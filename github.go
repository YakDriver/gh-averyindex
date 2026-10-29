package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	// Search runs an issue/PR search and returns the total count and up to n
	// items. order is "asc" or "desc" by creation time, or "" for best match.
	Search(query, order string, n int) (total int, items []searchItem, err error)
	// MergedPRs returns the total count and up to n PRs matching query, with
	// commits.
	MergedPRs(query string, n int) (total int, prs []rawPR, err error)
	// ThumbsUp sums 👍 reactions on the issues and PRs matching query.
	// GitHub search returns at most 1,000 results, so truncated reports
	// whether the sum is incomplete.
	ThumbsUp(query string) (total int, truncated bool, err error)
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

func (g *gitHub) Search(query, order string, n int) (int, []searchItem, error) {
	v := url.Values{}
	v.Set("q", query)
	v.Set("per_page", strconv.Itoa(max(1, n)))
	if order != "" {
		v.Set("sort", "created")
		v.Set("order", order)
	}
	var resp struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			Number        int             `json:"number"`
			CreatedAt     time.Time       `json:"created_at"`
			PullRequest   json.RawMessage `json:"pull_request"`
			RepositoryURL string          `json:"repository_url"`
		} `json:"items"`
	}
	if err := g.get("search/issues?"+v.Encode(), &resp); err != nil {
		return 0, nil, fmt.Errorf("search %q: %w", query, err)
	}
	items := make([]searchItem, 0, len(resp.Items))
	for _, it := range resp.Items[:min(n, len(resp.Items))] {
		items = append(items, searchItem{
			Number:    it.Number,
			CreatedAt: it.CreatedAt,
			IsPR:      len(it.PullRequest) > 0,
			Repo:      repoFromURL(it.RepositoryURL),
		})
	}
	return resp.TotalCount, items, nil
}

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

// withRetry runs call, waiting and retrying when GitHub rate limits it. Every
// wait is announced so a long pause doesn't look like a hang.
func withRetry(call func() error) error {
	for attempt := 1; ; attempt++ {
		err := call()
		wait, ok := rateLimitWait(err, time.Now())
		if !ok {
			return err
		}
		if attempt > maxRetries {
			fmt.Fprintf(notices, "GitHub rate limit: still limited after %d retries, giving up. Try again in a few minutes.\n", maxRetries)
			return err
		}
		if wait > maxRetryWait {
			fmt.Fprintf(notices, "GitHub rate limit: reset is %s away, longer than the %s this tool will wait. Try again later.\n",
				wait.Round(time.Second), maxRetryWait)
			return err
		}
		fmt.Fprintln(notices, retryNotice(wait, attempt, time.Now()))
		sleep(wait)
	}
}

func retryNotice(wait time.Duration, attempt int, now time.Time) string {
	return fmt.Sprintf("GitHub rate limit hit. Waiting %s (until %s) before retrying, attempt %d of %d. Not hung.",
		wait.Round(time.Second), now.Add(wait).Format("15:04:05"), attempt, maxRetries)
}

// rateLimitWait reports how long to wait before retrying err, if err is a
// rate limit response.
func rateLimitWait(err error, now time.Time) (time.Duration, bool) {
	// GraphQL reports its rate limit as an error in a 200 response.
	var gqlErr *api.GraphQLError
	if errors.As(err, &gqlErr) {
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

// repoFromURL turns "https://api.github.com/repos/owner/name" into "owner/name".
func repoFromURL(u string) string {
	if _, after, ok := strings.Cut(u, "/repos/"); ok {
		return after
	}
	return u
}

// maxCommits is how many commits per PR are fetched for rework.
const maxCommits = 100

const mergedPRsQuery = `
query($q: String!, $n: Int!) {
  search(query: $q, type: ISSUE, first: $n) {
    issueCount
    nodes {
      ... on PullRequest {
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
        }
      }
    }
  }
}`

func (g *gitHub) MergedPRs(query string, n int) (int, []rawPR, error) {
	var resp struct {
		Search struct {
			IssueCount int
			Nodes      []struct {
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
		}
	}
	vars := map[string]any{"q": query, "n": n}
	if err := g.gqlDo(mergedPRsQuery, vars, &resp); err != nil {
		return 0, nil, fmt.Errorf("merged PRs for %q: %w", query, err)
	}

	prs := make([]rawPR, 0, len(resp.Search.Nodes))
	for _, node := range resp.Search.Nodes {
		pr := rawPR{
			Number:           node.Number,
			MergedAt:         node.MergedAt,
			Additions:        node.Additions,
			Deletions:        node.Deletions,
			CommitsTruncated: node.Commits.TotalCount > maxCommits,
		}
		for _, cn := range node.Commits.Nodes {
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
		prs = append(prs, pr)
	}
	return resp.Search.IssueCount, prs, nil
}

const thumbsUpQuery = `
query($q: String!, $cursor: String) {
  search(query: $q, type: ISSUE, first: 100, after: $cursor) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on Issue { reactions(content: THUMBS_UP) { totalCount } }
      ... on PullRequest { reactions(content: THUMBS_UP) { totalCount } }
    }
  }
}`

// searchResultLimit is the most results GitHub search will return for a query.
const searchResultLimit = 1000

func (g *gitHub) ThumbsUp(query string) (int, bool, error) {
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
					}
				}
			}
		}
		vars := map[string]any{"q": query, "cursor": cursor}
		if err := g.gqlDo(thumbsUpQuery, vars, &resp); err != nil {
			return 0, false, fmt.Errorf("reactions for %q: %w", query, err)
		}
		for _, n := range resp.Search.Nodes {
			total += n.Reactions.TotalCount
		}
		if !resp.Search.PageInfo.HasNextPage {
			return total, resp.Search.IssueCount > searchResultLimit, nil
		}
		next := resp.Search.PageInfo.EndCursor
		cursor = &next
	}
}
