package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// searchItem is one issue or PR from a search result.
type searchItem struct {
	Number    int       `json:"number"`
	CreatedAt time.Time `json:"created_at"`
	IsPR      bool      `json:"is_pr"`
	Repo      string    `json:"repo,omitempty"` // owner/name
	Author    string    `json:"author,omitempty"`
	AuthorBot bool      `json:"author_bot,omitempty"`
}

func (s searchItem) kind() string {
	if s.IsPR {
		return "PR"
	}
	return "issue"
}

// rawCommit is one commit on a merged PR, as GitHub reports it.
type rawCommit struct {
	Logins    []string // GitHub users linked to the commit's authors and co-authors
	Additions int
	Deletions int
	Merge     bool // more than one parent, such as merging main into the branch
}

// rawPR is a merged PR with its commits.
type rawPR struct {
	Number           int
	MergedAt         time.Time
	Additions        int
	Deletions        int
	Commits          []rawCommit
	CommitsTruncated bool
}

// source is the subset of GitHub data that collect needs. It exists so
// collect can be tested without the network.
type source interface {
	// SearchMany runs several issue/PR searches together and returns a
	// result for each request, in order.
	SearchMany(reqs []searchReq) ([]searchResult, error)
	// SearchNext returns the page of up to 100 results after cursor, as
	// returned in a previous result's EndCursor.
	SearchNext(query, order, cursor string) (searchResult, error)
	// MergedPRs returns the total count of merged PRs matching query and the
	// n most recently merged of them in repo, with commits.
	MergedPRs(repo, query string, n int) (total int, prs []rawPR, err error)
	// OwnedRepoStars returns the star counts of the user's public, non-fork
	// repositories, at most the 100 most starred.
	OwnedRepoStars(login string) ([]int, error)
	// ThumbsUp sums 👍 reactions on the issues and PRs matching query, not
	// counting reactions by author.
	// GitHub search returns at most 1,000 results, so truncated reports
	// whether the sum is incomplete.
	ThumbsUp(query, author string) (total int, truncated bool, err error)
}

// searchReq is one issue/PR search. Order is "asc" or "desc" by creation
// time, or "" for best match. N is how many items to return; 0 returns only
// the count.
type searchReq struct {
	Query string
	Order string
	N     int
}

type searchResult struct {
	Total int
	Items []searchItem
	// EndCursor continues an item search with SearchNext when HasNext.
	EndCursor string
	HasNext   bool
}

type gitHub struct {
	rest *api.RESTClient
	gql  *api.GraphQLClient
}

func newGitHub(host string) (*gitHub, error) {
	opts := api.ClientOptions{Host: host}
	rest, err := api.NewRESTClient(opts)
	if err != nil {
		return nil, err
	}
	gql, err := api.NewGraphQLClient(opts)
	if err != nil {
		return nil, err
	}
	return &gitHub{rest: rest, gql: gql}, nil
}

// account is a GitHub user's canonical login and creation time.
type account struct {
	Login     string    `json:"login"`
	CreatedAt time.Time `json:"created_at"`
	// Type is "User", "Bot", or "Organization".
	Type string `json:"type"`
}

// item is the PR or issue whose author is being scored.
type item struct {
	Author string
	Title  string
	IsPR   bool
}

func (i item) kind() string {
	if i.IsPR {
		return "PR"
	}
	return "issue"
}

// Item looks up a PR or issue by number. GitHub's issues API returns both.
func (g *gitHub) Item(repo string, number int) (item, error) {
	var resp struct {
		Title string `json:"title"`
		User  *struct {
			Login string `json:"login"`
		} `json:"user"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	err := g.get(fmt.Sprintf("repos/%s/issues/%d", repo, number), &resp)
	if httpErr, ok := errors.AsType[*api.HTTPError](err); ok && httpErr.StatusCode == http.StatusNotFound {
		return item{}, fmt.Errorf("no pull request or issue #%d in %s", number, repo)
	}
	if err != nil {
		return item{}, err
	}
	// Deleted accounts show up as the placeholder user "ghost".
	if resp.User == nil || resp.User.Login == "" || resp.User.Login == "ghost" {
		return item{}, fmt.Errorf("#%d in %s has no author (deleted account?)", number, repo)
	}
	return item{Author: resp.User.Login, Title: resp.Title, IsPR: len(resp.PullRequest) > 0}, nil
}

// Account looks up user, or returns an error if it doesn't exist.
func (g *gitHub) Account(user string) (account, error) {
	var resp account
	err := g.get("users/"+url.PathEscape(user), &resp)
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return resp, fmt.Errorf("user %q not found", user)
	}
	return resp, err
}

// SearchMany sends every search in one GraphQL request, using an alias for
// each. One request is faster than many REST calls, and GraphQL search
// isn't subject to the REST search limit of 30 requests per minute (#26).
func (g *gitHub) SearchMany(reqs []searchReq) ([]searchResult, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	var q strings.Builder
	vars := map[string]any{}
	q.WriteString("query(")
	for i := range reqs {
		if i > 0 {
			q.WriteString(", ")
		}
		fmt.Fprintf(&q, "$q%d: String!", i)
	}
	q.WriteString(") {\n")
	for i, r := range reqs {
		vars[fmt.Sprintf("q%d", i)] = r.Query + graphQLSort(r.Order)
		fmt.Fprintf(&q, "  s%d: search(query: $q%d, type: ISSUE, first: %d) { issueCount", i, i, min(r.N, searchPageSize))
		if r.N > 0 {
			q.WriteString(" pageInfo { hasNextPage endCursor } nodes { __typename ...item }")
		}
		q.WriteString(" }\n")
	}
	q.WriteString("}\n")
	// GraphQL rejects a fragment that isn't used, so a batch of counts only
	// leaves it out.
	if slices.ContainsFunc(reqs, func(r searchReq) bool { return r.N > 0 }) {
		q.WriteString(itemFragments)
	}

	var resp map[string]searchNode
	if err := g.gqlDo(q.String(), vars, &resp); err != nil {
		return nil, fmt.Errorf("searches: %w", err)
	}
	results := make([]searchResult, len(reqs))
	for i := range reqs {
		results[i] = resp[fmt.Sprintf("s%d", i)].result()
	}
	return results, nil
}

type searchNode struct {
	IssueCount int
	PageInfo   struct {
		HasNextPage bool
		EndCursor   string
	}
	Nodes []itemNode
}

func (n searchNode) result() searchResult {
	r := searchResult{Total: n.IssueCount, HasNext: n.PageInfo.HasNextPage, EndCursor: n.PageInfo.EndCursor}
	for _, it := range n.Nodes {
		if it.Typename == "Issue" || it.Typename == "PullRequest" {
			r.Items = append(r.Items, it.item())
		}
	}
	return r
}

const searchNextQuery = `
query($q: String!, $cursor: String!) {
  search(query: $q, type: ISSUE, first: 100, after: $cursor) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes { __typename ...item }
  }
}
` + itemFragments

// SearchNext continues a search in GraphQL, so paging doesn't use the REST
// search limit either.
func (g *gitHub) SearchNext(query, order, cursor string) (searchResult, error) {
	var resp struct{ Search searchNode }
	vars := map[string]any{"q": query + graphQLSort(order), "cursor": cursor}
	if err := g.gqlDo(searchNextQuery, vars, &resp); err != nil {
		return searchResult{}, fmt.Errorf("search %q: %w", query, err)
	}
	return resp.Search.result(), nil
}

// graphQLSort turns an order into a search qualifier. GraphQL search has no
// separate sort argument.
func graphQLSort(order string) string {
	switch order {
	case "asc":
		return " sort:created-asc"
	case "desc":
		return " sort:created-desc"
	}
	return ""
}

const itemFragments = `fragment item on SearchResultItem {
  ... on Issue { number createdAt repository { nameWithOwner } author { login __typename } }
  ... on PullRequest { number createdAt repository { nameWithOwner } author { login __typename } }
}`

type itemNode struct {
	Typename   string `json:"__typename"`
	Number     int
	CreatedAt  time.Time
	Repository struct{ NameWithOwner string }
	Author     *struct {
		Login    string
		Typename string `json:"__typename"`
	}
}

func (n itemNode) item() searchItem {
	it := searchItem{
		Number:    n.Number,
		CreatedAt: n.CreatedAt,
		IsPR:      n.Typename == "PullRequest",
		Repo:      n.Repository.NameWithOwner,
	}
	if n.Author != nil {
		it.Author = n.Author.Login
		it.AuthorBot = n.Author.Typename == "Bot"
	}
	return it
}

// searchPageSize is the most results the search API returns per page.
const searchPageSize = 100

const (
	// maxRetries is how many times a rate-limited request is retried.
	maxRetries = 3
	// maxRetryWait is the longest we'll wait before retrying. Longer waits
	// return the error instead.
	maxRetryWait = 2 * time.Minute
)

// notices is where rate limit waits are reported. It's stderr so --json
// output stays clean.
var notices io.Writer = os.Stderr

// sleep is time.Sleep, replaceable in tests.
var sleep = time.Sleep

// get is rest.Get with retries when GitHub rate limits the request. Search
// has a secondary rate limit that trips easily when scoring several users in
// a row.
func (g *gitHub) get(path string, v any) error {
	return withRetry(func() error { return g.rest.Get(path, v) })
}

// gqlDo is gql.Do with the same retries.
func (g *gitHub) gqlDo(query string, vars map[string]any, v any) error {
	return withRetry(func() error { return g.gql.Do(query, vars, v) })
}

// withRetry runs call, waiting and retrying when GitHub rate limits it or
// returns a transient server error. Every wait is announced so a long pause
// doesn't look like a hang.
func withRetry(call func() error) error {
	for attempt := 1; ; attempt++ {
		err := call()
		wait, ok := rateLimitWait(err, time.Now())
		reason := "GitHub rate limit hit."
		if !ok {
			wait, ok = transientWait(err, attempt)
			reason = "GitHub returned a server error (a slow query can time out)."
		}
		if !ok {
			return err
		}
		if attempt > maxRetries {
			fmt.Fprintf(notices, "GitHub: still failing after %d retries, giving up. Try again in a few minutes.\n", maxRetries)
			return err
		}
		if wait > maxRetryWait {
			fmt.Fprintf(notices, "GitHub rate limit: reset is %s away, longer than the %s this tool will wait. Try again later.\n",
				wait.Round(time.Second), maxRetryWait)
			return err
		}
		fmt.Fprintln(notices, retryNotice(reason, wait, attempt, time.Now()))
		sleep(wait)
	}
}

func retryNotice(reason string, wait time.Duration, attempt int, now time.Time) string {
	return fmt.Sprintf("%s Waiting %s (until %s) before retrying, attempt %d of %d. Not hung.",
		reason, wait.Round(time.Second), now.Add(wait).Format("15:04:05"), attempt, maxRetries)
}

// transientWait reports how long to wait before retrying a 502, 503, or 504.
// GitHub returns these when a request times out or a server is briefly
// unavailable, and they usually succeed on retry.
func transientWait(err error, attempt int) (time.Duration, bool) {
	h, ok := errors.AsType[*api.HTTPError](err)
	if !ok {
		return 0, false
	}
	switch h.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return time.Duration(attempt*attempt) * 2 * time.Second, true
	}
	return 0, false
}

// rateLimitWait reports how long to wait before retrying err, if err is a
// rate limit response.
func rateLimitWait(err error, now time.Time) (time.Duration, bool) {
	// GraphQL reports its rate limit as an error in a 200 response.
	if gqlErr, ok := errors.AsType[*api.GraphQLError](err); ok {
		for _, e := range gqlErr.Errors {
			if e.Type == "RATE_LIMITED" {
				return time.Minute, true
			}
		}
		return 0, false
	}

	var h *api.HTTPError
	if !errors.As(err, &h) || (h.StatusCode != http.StatusForbidden && h.StatusCode != http.StatusTooManyRequests) {
		return 0, false
	}
	if s, err := strconv.Atoi(h.Headers.Get("Retry-After")); err == nil {
		return time.Duration(s) * time.Second, true
	}
	if h.Headers.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(h.Headers.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return max(time.Second, time.Unix(reset, 0).Sub(now)+time.Second), true
		}
	}
	// Secondary rate limits don't always send headers. A 403 for missing
	// permissions doesn't mention rate limits, so it isn't retried.
	if strings.Contains(strings.ToLower(h.Message), "rate limit") {
		return time.Minute, true
	}
	return 0, false
}

// maxCommits is how many commits per PR are fetched for rework.
const maxCommits = 100

// mergedCandidates is how many recently updated merged PRs are considered
// when picking the most recently merged. Search can't sort by merge time, but
// a PR's update time is at or after its merge time, so the most recently
// merged PRs are near the top of the most recently updated. Extra candidates
// cover old PRs that got a comment recently.
const mergedCandidates = 25

const mergedCandidatesQuery = `
query($q: String!, $n: Int!) {
  search(query: $q, type: ISSUE, first: $n) {
    issueCount
    nodes { ... on PullRequest { number mergedAt } }
  }
}`

// prFields is what rework needs from each PR.
const prFields = `
number
mergedAt
additions
deletions
commits(first: 100) {
  totalCount
  nodes {
    commit {
      additions
      deletions
      parents(first: 2) { totalCount }
      authors(first: 10) { nodes { user { login } } }
    }
  }
}`

type prNode struct {
	Number    int
	MergedAt  time.Time
	Additions int
	Deletions int
	Commits   struct {
		TotalCount int
		Nodes      []struct {
			Commit struct {
				Additions int
				Deletions int
				Parents   struct{ TotalCount int }
				Authors   struct {
					Nodes []struct {
						User *struct{ Login string }
					}
				}
			}
		}
	}
}

func (n prNode) raw() rawPR {
	pr := rawPR{
		Number:           n.Number,
		MergedAt:         n.MergedAt,
		Additions:        n.Additions,
		Deletions:        n.Deletions,
		CommitsTruncated: n.Commits.TotalCount > maxCommits,
	}
	for _, cn := range n.Commits.Nodes {
		c := rawCommit{
			Additions: cn.Commit.Additions,
			Deletions: cn.Commit.Deletions,
			Merge:     cn.Commit.Parents.TotalCount > 1,
		}
		for _, a := range cn.Commit.Authors.Nodes {
			if a.User != nil {
				c.Logins = append(c.Logins, a.User.Login)
			}
		}
		pr.Commits = append(pr.Commits, c)
	}
	return pr
}

// mostRecentlyMerged returns the numbers of the n most recently merged PRs,
// newest first.
func mostRecentlyMerged(candidates []rawPR, n int) []int {
	sorted := slices.Clone(candidates)
	slices.SortStableFunc(sorted, func(a, b rawPR) int { return b.MergedAt.Compare(a.MergedAt) })
	var numbers []int
	for _, pr := range sorted[:min(n, len(sorted))] {
		numbers = append(numbers, pr.Number)
	}
	return numbers
}

func (g *gitHub) MergedPRs(repo, query string, n int) (int, []rawPR, error) {
	var cand struct {
		Search struct {
			IssueCount int
			Nodes      []struct {
				Number   int
				MergedAt time.Time
			}
		}
	}
	vars := map[string]any{"q": query + " sort:updated-desc", "n": mergedCandidates}
	if err := g.gqlDo(mergedCandidatesQuery, vars, &cand); err != nil {
		return 0, nil, fmt.Errorf("merged PRs for %q: %w", query, err)
	}
	var candidates []rawPR
	for _, c := range cand.Search.Nodes {
		candidates = append(candidates, rawPR{Number: c.Number, MergedAt: c.MergedAt})
	}
	numbers := mostRecentlyMerged(candidates, n)
	if len(numbers) == 0 {
		return cand.Search.IssueCount, nil, nil
	}

	owner, name, _ := strings.Cut(repo, "/")
	var q strings.Builder
	q.WriteString("query($owner: String!, $name: String!) {\n  repository(owner: $owner, name: $name) {\n")
	for i, num := range numbers {
		fmt.Fprintf(&q, "    pr%d: pullRequest(number: %d) {%s}\n", i, num, prFields)
	}
	q.WriteString("  }\n}")

	var resp struct {
		Repository map[string]*prNode
	}
	if err := g.gqlDo(q.String(), map[string]any{"owner": owner, "name": name}, &resp); err != nil {
		return 0, nil, fmt.Errorf("commits for merged PRs in %s: %w", repo, err)
	}
	prs := make([]rawPR, 0, len(numbers))
	for i := range numbers {
		if node := resp.Repository[fmt.Sprintf("pr%d", i)]; node != nil {
			prs = append(prs, node.raw())
		}
	}
	return cand.Search.IssueCount, prs, nil
}

const ownedReposQuery = `
query($login: String!) {
  user(login: $login) {
    repositories(ownerAffiliations: OWNER, isFork: false, privacy: PUBLIC, first: 100,
                 orderBy: {field: STARGAZERS, direction: DESC}) {
      nodes { stargazerCount }
    }
  }
}`

func (g *gitHub) OwnedRepoStars(login string) ([]int, error) {
	var resp struct {
		User *struct {
			Repositories struct {
				Nodes []struct{ StargazerCount int }
			}
		}
	}
	if err := g.gqlDo(ownedReposQuery, map[string]any{"login": login}, &resp); err != nil {
		return nil, fmt.Errorf("repositories for %q: %w", login, err)
	}
	if resp.User == nil {
		return nil, nil
	}
	stars := make([]int, 0, len(resp.User.Repositories.Nodes))
	for _, n := range resp.User.Repositories.Nodes {
		stars = append(stars, n.StargazerCount)
	}
	return stars, nil
}

// reactionSample is how many 👍 reactions per item are checked for the
// author's own. An author who upvotes their own issue usually does it first,
// so checking the oldest few is enough. Fetching more made each page slow
// enough for GitHub to time it out with a 502 on users with thousands of
// items (#24).
const reactionSample = 10

const thumbsUpQuery = `
query($q: String!, $cursor: String, $sample: Int!) {
  search(query: $q, type: ISSUE, first: 100, after: $cursor) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on Issue { reactions(content: THUMBS_UP, first: $sample, orderBy: {field: CREATED_AT, direction: ASC}) { ...thumbs } }
      ... on PullRequest { reactions(content: THUMBS_UP, first: $sample, orderBy: {field: CREATED_AT, direction: ASC}) { ...thumbs } }
    }
  }
}

fragment thumbs on ReactionConnection {
  totalCount
  nodes { user { login } }
}`

// othersReactions is total reactions minus any by author among the sampled
// logins.
func othersReactions(total int, logins []string, author string) int {
	for _, l := range logins {
		if strings.EqualFold(l, author) {
			total--
		}
	}
	return max(0, total)
}

// searchResultLimit is the most results GitHub search will return for a query.
const searchResultLimit = 1000

func (g *gitHub) ThumbsUp(query, author string) (int, bool, error) {
	var total int
	var cursor *string
	for {
		var resp struct {
			Search struct {
				IssueCount int
				PageInfo   struct {
					HasNextPage bool
					EndCursor   string
				}
				Nodes []struct {
					Reactions struct {
						TotalCount int
						Nodes      []struct {
							User *struct{ Login string }
						}
					}
				}
			}
		}
		vars := map[string]any{"q": query, "cursor": cursor, "sample": reactionSample}
		if err := g.gqlDo(thumbsUpQuery, vars, &resp); err != nil {
			return 0, false, fmt.Errorf("reactions for %q: %w", query, err)
		}
		for _, n := range resp.Search.Nodes {
			var logins []string
			for _, r := range n.Reactions.Nodes {
				if r.User != nil {
					logins = append(logins, r.User.Login)
				}
			}
			total += othersReactions(n.Reactions.TotalCount, logins, author)
		}
		if !resp.Search.PageInfo.HasNextPage {
			return total, resp.Search.IssueCount > searchResultLimit, nil
		}
		next := resp.Search.PageInfo.EndCursor
		cursor = &next
	}
}
