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
