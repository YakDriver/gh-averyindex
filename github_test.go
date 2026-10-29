package main

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

// graphQLFunc answers GraphQL requests in tests without the network.
type graphQLFunc func(t *testing.T, query string, vars map[string]any) string

func (f graphQLFunc) client(t *testing.T) *gitHub {
	t.Helper()
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(f(t, body.Query, body.Variables))),
			Request:    req,
		}, nil
	})
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: "github.com", AuthToken: "test", Transport: rt})
	if err != nil {
		t.Fatal(err)
	}
	return &gitHub{gql: gql}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMergedPRsFetchesCommitsOnlyForMostRecentlyMerged(t *testing.T) {
	var commitQueries int
	gh := graphQLFunc(func(t *testing.T, query string, vars map[string]any) string {
		switch {
		case strings.Contains(query, "search("):
			if q := vars["q"]; q != "repo:o/r is:pr author:u is:merged sort:updated-desc" {
				t.Errorf("candidate search q = %v", q)
			}
			if strings.Contains(query, "commits") {
				t.Error("candidate search should not fetch commits")
			}
			// Update order: #3 is old but was commented on today, and #9 was
			// opened long ago but merged most recently.
			return `{"data":{"search":{"issueCount":40,"nodes":[
				{"number":3,"mergedAt":"2024-01-01T00:00:00Z"},
				{"number":7,"mergedAt":"2026-10-20T00:00:00Z"},
				{"number":9,"mergedAt":"2026-10-28T00:00:00Z"},
				{"number":8,"mergedAt":"2026-10-25T00:00:00Z"}]}}}`

		case strings.Contains(query, "repository("):
			commitQueries++
			if vars["owner"] != "o" || vars["name"] != "r" {
				t.Errorf("repository vars = %v", vars)
			}
			var asked []int
			for _, m := range regexp.MustCompile(`pullRequest\(number: (\d+)\)`).FindAllStringSubmatch(query, -1) {
				n, _ := strconv.Atoi(m[1])
				asked = append(asked, n)
			}
			if !slices.Equal(asked, []int{9, 8}) {
				t.Errorf("commits requested for %v, want [9 8] only", asked)
			}
			// Aliases arrive in the opposite order from the query.
			return `{"data":{"repository":{
				"pr1":{"number":8,"mergedAt":"2026-10-25T00:00:00Z","additions":10,"deletions":0,
					"commits":{"totalCount":1,"nodes":[{"commit":{"additions":10,"deletions":0,
						"parents":{"totalCount":1},"authors":{"nodes":[{"user":{"login":"u"}}]}}}]}},
				"pr0":{"number":9,"mergedAt":"2026-10-28T00:00:00Z","additions":100,"deletions":5,
					"commits":{"totalCount":2,"nodes":[
						{"commit":{"additions":100,"deletions":0,"parents":{"totalCount":1},"authors":{"nodes":[{"user":{"login":"u"}}]}}},
						{"commit":{"additions":0,"deletions":5,"parents":{"totalCount":2},"authors":{"nodes":[{"user":null}]}}}]}}}}}`
		}
		t.Fatalf("unexpected query: %s", query)
		return ""
	}).client(t)

	total, prs, err := gh.MergedPRs("o/r", "repo:o/r is:pr author:u is:merged", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 40 {
		t.Errorf("total = %d, want 40", total)
	}
	if commitQueries != 1 {
		t.Errorf("commit queries = %d, want 1", commitQueries)
	}
	var numbers []int
	for _, pr := range prs {
		numbers = append(numbers, pr.Number)
	}
	if !slices.Equal(numbers, []int{9, 8}) {
		t.Fatalf("PRs = %v, want [9 8] in merge order", numbers)
	}
	pr := prs[0]
	if len(pr.Commits) != 2 || !slices.Equal(pr.Commits[0].Logins, []string{"u"}) ||
		pr.Commits[1].Logins != nil || !pr.Commits[1].Merge || pr.Commits[0].Merge {
		t.Errorf("#9 commits decoded wrong: %+v", pr.Commits)
	}
}

func TestThumbsUpSamplesAndSkipsSelf(t *testing.T) {
	pages := 0
	gh := graphQLFunc(func(t *testing.T, query string, vars map[string]any) string {
		pages++
		if vars["sample"] != float64(reactionSample) {
			t.Errorf("sample = %v, want %d", vars["sample"], reactionSample)
		}
		if pages == 1 {
			return `{"data":{"search":{"issueCount":2,"pageInfo":{"hasNextPage":true,"endCursor":"c1"},"nodes":[
				{"reactions":{"totalCount":3,"nodes":[{"user":{"login":"U"}},{"user":{"login":"a"}},{"user":null}]}}]}}}`
		}
		if vars["cursor"] != "c1" {
			t.Errorf("cursor = %v, want c1", vars["cursor"])
		}
		return `{"data":{"search":{"issueCount":2,"pageInfo":{"hasNextPage":false},"nodes":[
			{"reactions":{"totalCount":0,"nodes":[]}},{}]}}}`
	}).client(t)

	total, truncated, err := gh.ThumbsUp("repo:o/r author:u", "u")
	if err != nil || total != 2 || truncated || pages != 2 {
		t.Errorf("ThumbsUp() = %d, %v, %v after %d pages; want 2, false, nil after 2", total, truncated, err, pages)
	}
}

func TestSearchManyOneRequest(t *testing.T) {
	requests := 0
	gh := graphQLFunc(func(t *testing.T, query string, vars map[string]any) string {
		requests++
		// Queries go in variables, so quotes and colons need no escaping.
		want := map[string]any{
			"q0": `repo:o/r is:pr author:u "quoted"`,
			"q1": "repo:o/r author:u sort:created-asc",
			"q2": "repo:o/r author:u sort:created-desc",
		}
		for k, v := range want {
			if vars[k] != v {
				t.Errorf("%s = %v, want %v", k, vars[k], v)
			}
		}
		if !strings.Contains(query, "s0: search(query: $q0, type: ISSUE, first: 0) { issueCount }") {
			t.Errorf("count search should ask for no nodes:\n%s", query)
		}
		if !strings.Contains(query, "s2: search(query: $q2, type: ISSUE, first: 100) { issueCount pageInfo { hasNextPage endCursor } nodes") {
			t.Errorf("item search should be capped at 100 per page:\n%s", query)
		}
		// Aliases come back in a different order than they were sent.
		return `{"data":{
			"s2":{"issueCount":250,"nodes":[
				{"__typename":"PullRequest","number":9,"createdAt":"2026-10-28T00:00:00Z",
				 "repository":{"nameWithOwner":"o/r"},"author":{"login":"u","__typename":"User"}},
				{"__typename":"Issue","number":8,"createdAt":"2026-10-27T00:00:00Z",
				 "repository":{"nameWithOwner":"a/b"},"author":{"login":"dependabot","__typename":"Bot"}},
				{"__typename":"Issue","number":7,"createdAt":"2026-10-26T00:00:00Z",
				 "repository":{"nameWithOwner":"a/b"},"author":null}]},
			"s0":{"issueCount":12},
			"s1":{"issueCount":250,"nodes":[{"__typename":"Issue","number":1,"createdAt":"2017-01-01T00:00:00Z",
				 "repository":{"nameWithOwner":"o/r"},"author":{"login":"u","__typename":"User"}}]}}}`
	}).client(t)

	results, err := gh.SearchMany([]searchReq{
		{Query: `repo:o/r is:pr author:u "quoted"`},
		{Query: "repo:o/r author:u", Order: "asc", N: 1},
		{Query: "repo:o/r author:u", Order: "desc", N: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Errorf("%d requests, want 1", requests)
	}
	if len(results) != 3 || results[0].Total != 12 || results[0].Items != nil {
		t.Fatalf("count result = %+v", results)
	}
	if got := results[1].Items; len(got) != 1 || got[0].Number != 1 || got[0].IsPR {
		t.Errorf("first authored = %+v", got)
	}
	want := []searchItem{
		{Number: 9, CreatedAt: day("2026-10-28"), IsPR: true, Repo: "o/r", Author: "u"},
		{Number: 8, CreatedAt: day("2026-10-27"), Repo: "a/b", Author: "dependabot", AuthorBot: true},
		{Number: 7, CreatedAt: day("2026-10-26"), Repo: "a/b"},
	}
	if results[2].Total != 250 || !slices.Equal(results[2].Items, want) {
		t.Errorf("items = %+v (total %d)", results[2].Items, results[2].Total)
	}
}

func TestSearchManyCountsOnly(t *testing.T) {
	// Copilot on #27: GraphQL rejects a fragment that isn't used.
	gh := graphQLFunc(func(t *testing.T, query string, _ map[string]any) string {
		if strings.Contains(query, "fragment") || strings.Contains(query, "pageInfo") {
			t.Errorf("count-only batch includes item fields:\n%s", query)
		}
		return `{"data":{"s0":{"issueCount":4},"s1":{"issueCount":0}}}`
	}).client(t)
	results, err := gh.SearchMany([]searchReq{{Query: "a"}, {Query: "b"}})
	if err != nil || len(results) != 2 || results[0].Total != 4 || results[1].Total != 0 {
		t.Errorf("SearchMany() = %+v, %v", results, err)
	}
}

func TestSearchNext(t *testing.T) {
	gh := graphQLFunc(func(t *testing.T, query string, vars map[string]any) string {
		if vars["q"] != "is:pr author:u sort:created-desc" || vars["cursor"] != "c1" {
			t.Errorf("vars = %v", vars)
		}
		if !strings.Contains(query, "after: $cursor") || !strings.Contains(query, "fragment item") {
			t.Errorf("query:\n%s", query)
		}
		return `{"data":{"search":{"issueCount":150,"pageInfo":{"hasNextPage":false,"endCursor":"c2"},
			"nodes":[{"__typename":"PullRequest","number":3,"createdAt":"2026-10-01T00:00:00Z",
			"repository":{"nameWithOwner":"x/y"},"author":{"login":"u","__typename":"User"}}]}}}`
	}).client(t)
	r, err := gh.SearchNext("is:pr author:u", "desc", "c1")
	if err != nil || r.Total != 150 || r.HasNext || len(r.Items) != 1 || r.Items[0].Repo != "x/y" {
		t.Errorf("SearchNext() = %+v, %v", r, err)
	}
}

func restClient(t *testing.T, status int, body string, wantPath string) *gitHub {
	t.Helper()
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", req.URL.Path, wantPath)
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	rest, err := api.NewRESTClient(api.ClientOptions{Host: "github.com", AuthToken: "test", Transport: rt})
	if err != nil {
		t.Fatal(err)
	}
	return &gitHub{rest: rest}
}

func TestItem(t *testing.T) {
	const path = "/repos/o/r/issues/7"
	tests := []struct {
		name    string
		status  int
		body    string
		want    item
		wantErr string
	}{
		{"pull request", 200, `{"title":"Fix it","user":{"login":"alice"},"pull_request":{"url":"x"}}`,
			item{Author: "alice", Title: "Fix it", IsPR: true}, ""},
		{"issue", 200, `{"title":"Broken","user":{"login":"bob"}}`, item{Author: "bob", Title: "Broken"}, ""},
		{"deleted author", 200, `{"title":"Old","user":{"login":"ghost"}}`, item{}, "no author"},
		{"not found", 404, `{"message":"Not Found"}`, item{}, "no pull request or issue #7 in o/r"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := restClient(t, tt.status, tt.body, path).Item("o/r", 7)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Item() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Item() = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestMergedPRsNoneMerged(t *testing.T) {
	gh := graphQLFunc(func(t *testing.T, query string, _ map[string]any) string {
		if !strings.Contains(query, "search(") {
			t.Errorf("commits fetched with no merged PRs: %s", query)
		}
		return `{"data":{"search":{"issueCount":0,"nodes":[]}}}`
	}).client(t)

	total, prs, err := gh.MergedPRs("o/r", "q", 5)
	if err != nil || total != 0 || prs != nil {
		t.Errorf("MergedPRs() = %d, %v, %v", total, prs, err)
	}
}
