package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// searchItem is one issue or PR from a search result.
type searchItem struct {
	Number    int       `json:"number"`
	CreatedAt time.Time `json:"created_at"`
	IsPR      bool      `json:"is_pr"`
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

// Login returns the canonical login for user, or an error if it doesn't exist.
func (g *gitHub) Login(user string) (string, error) {
	var resp struct {
		Login string `json:"login"`
	}
	err := g.rest.Get("users/"+url.PathEscape(user), &resp)
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("user %q not found", user)
	}
	if err != nil {
		return "", err
	}
	return resp.Login, nil
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
			Number      int             `json:"number"`
			CreatedAt   time.Time       `json:"created_at"`
			PullRequest json.RawMessage `json:"pull_request"`
		} `json:"items"`
	}
	if err := g.rest.Get("search/issues?"+v.Encode(), &resp); err != nil {
		return 0, nil, fmt.Errorf("search %q: %w", query, err)
	}
	items := make([]searchItem, 0, len(resp.Items))
	for _, it := range resp.Items[:min(n, len(resp.Items))] {
		items = append(items, searchItem{Number: it.Number, CreatedAt: it.CreatedAt, IsPR: len(it.PullRequest) > 0})
	}
	return resp.TotalCount, items, nil
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
	if err := g.gql.Do(mergedPRsQuery, vars, &resp); err != nil {
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
		if err := g.gql.Do(thumbsUpQuery, vars, &resp); err != nil {
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
