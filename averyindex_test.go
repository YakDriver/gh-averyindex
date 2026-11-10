package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cli/go-gh/v2/pkg/api"
)

type fakeSearch struct {
	total int
	items []searchItem
}

type fakeSource struct {
	mu          sync.Mutex            // collect sends two batches at once
	search      map[string]fakeSearch // keyed by order + "|" + query
	queries     []string
	merged      int
	prs         []rawPR
	mergedQuery string
	thumbs      int
	truncated   bool
	stars       []ownedRepo
	err         error

	thumbsQuery, thumbsAuthor string
	batches                   int
	reviewed                  []rawPR
	reviewedQuery             string
	reviewedErr               error
	details                   []string // queries of searches with extras
	comments, reviews         []engaged
	engagedSince              time.Time
	engageErr                 error
}

func (f *fakeSource) SearchMany(reqs []searchReq) ([]searchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches++
	var results []searchResult
	for _, r := range reqs {
		total, items, err := f.Search(r.Query, r.Order, r.N)
		if err != nil {
			return nil, err
		}
		if r.Extra != extraNone {
			f.details = append(f.details, r.Query)
		}
		results = append(results, searchResult{Total: total, Items: items})
	}
	return results, nil
}

func (f *fakeSource) OwnedRepos(string) ([]ownedRepo, error) {
	return f.stars, f.err
}

func (f *fakeSource) Search(q, order string, n int) (int, []searchItem, error) {
	key := order + "|" + q
	f.queries = append(f.queries, key)
	if f.err != nil {
		return 0, nil, f.err
	}
	r := f.search[key]
	return r.total, r.items[:min(n, len(r.items))], nil
}

func (f *fakeSource) MergedPRs(repo, q string, n int) (int, []rawPR, error) {
	f.mergedQuery = repo + " " + q
	return f.merged, f.prs[:min(n, len(f.prs))], nil
}

func (f *fakeSource) Engagement(login string, since time.Time) (activity, error) {
	f.engagedSince = since
	if f.engageErr != nil {
		return activity{}, f.engageErr
	}
	return activity{Comments: f.comments, Reviews: f.reviews}, nil
}

func (f *fakeSource) ReviewedPRs(repo, q string, minThreads int) ([]rawPR, error) {
	f.reviewedQuery = fmt.Sprintf("%s %s >=%d", repo, q, minThreads)
	return f.reviewed, cmp.Or(f.reviewedErr, f.err)
}

func (f *fakeSource) ThumbsUp(q, author string) (int, bool, error) {
	f.thumbsQuery, f.thumbsAuthor = q, author
	return f.thumbs, f.truncated, nil
}

var testNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

var testMeta = meta{Version: "v9.9.9", GeneratedAt: testNow}

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCollect(t *testing.T) {
	const r = "repo:o/r"
	const g = "is:pr author:u is:public -user:u -repo:o/r"
	first := searchItem{Number: 1, CreatedAt: day("2020-01-01")}
	last := searchItem{Number: 9, CreatedAt: day("2026-10-01"), IsPR: true}
	open := searchItem{Number: 9, CreatedAt: day("2026-10-01"), IsPR: true}
	src := &fakeSource{
		search: map[string]fakeSearch{
			"|" + r + " is:pr author:u":                             {total: 10},
			"|" + r + " is:issue author:u":                          {total: 4},
			"|" + r + " commenter:u -author:u -reviewed-by:u":       {total: 20},
			"|" + r + " is:pr reviewed-by:u -author:u":              {total: 5},
			"asc|" + r + " author:u":                                {total: 14, items: []searchItem{first}},
			"desc|" + r + " author:u":                               {total: 14, items: []searchItem{last}},
			"desc|" + r + " is:pr author:u is:open":                 {total: 1, items: []searchItem{open}},
			"|" + g:                                                 {total: 110},
			"|" + g + " is:merged":                                  {total: 40},
			"|" + g + " is:open":                                    {total: 30},
			"|" + g + " created:>=2026-09-29 is:merged":             {total: 2},
			"|" + g + " created:>=2026-09-29 is:closed is:unmerged": {total: 1},
			"desc|" + g + " created:>=2026-09-29": {total: 4, items: []searchItem{
				{Repo: "a/x"}, {Repo: "b/y"}, {Repo: "b/y"}, {Repo: "c/z"},
			}},
		},
		merged: 6,
		prs: []rawPR{
			{Number: 5, Commits: []rawCommit{{Logins: []string{"u"}, Additions: 100}}},
			{Number: 4, Commits: []rawCommit{{Logins: []string{"u"}, Additions: 100}, {Logins: []string{"m"}, Additions: 50}}},
		},
		thumbs: 9,
	}

	m, err := collect(src, "o/r", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatal(err)
	}

	if m.PRsOpened != 10 || m.PRsMerged != 6 || m.PRsOpen != 1 || m.Issues != 4 ||
		m.Threads != 20 || m.Reviews != 5 || m.ThumbsUp != 9 {
		t.Errorf("unexpected counts: %+v", m)
	}
	if m.PRsClosedUnmerged() != 3 {
		t.Errorf("PRsClosedUnmerged() = %d, want 3", m.PRsClosedUnmerged())
	}
	if m.FirstAuthored == nil || *m.FirstAuthored != first {
		t.Errorf("FirstAuthored = %+v, want %+v", m.FirstAuthored, first)
	}
	if m.LastAuthored == nil || *m.LastAuthored != last {
		t.Errorf("LastAuthored = %+v, want %+v", m.LastAuthored, last)
	}
	if !slices.Equal(m.OpenPRs, []searchItem{open}) {
		t.Errorf("OpenPRs = %+v", m.OpenPRs)
	}
	if want := "o/r " + r + " is:pr author:u is:merged"; src.mergedQuery != want {
		t.Errorf("merged query = %q, want %q", src.mergedQuery, want)
	}
	if len(m.RecentMerged) != 2 || m.RecentMerged[0].Credit != 1 || m.RecentMerged[1].Credit != 0.5 {
		t.Errorf("RecentMerged = %+v", m.RecentMerged)
	}
	if got := m.CodeCredit(); got != 4.5 {
		t.Errorf("CodeCredit() = %v, want 4.5 (6 merged × 0.75)", got)
	}
	wantGlobal := Global{PRsOpened: 110, PRsMerged: 40, PRsOpen: 30,
		RecentMerged: 2, RecentClosedUnmerged: 1,
		RecentPRs: 4, RecentRepos: 3, RecentTopRepo: "b/y", RecentTopCount: 2, RecentSampled: 4}
	if m.Global != wantGlobal {
		t.Errorf("Global =\n%+v\nwant\n%+v", m.Global, wantGlobal)
	}
	for key := range src.search {
		if !slices.Contains(src.queries, key) {
			t.Errorf("query not issued: %q", key)
		}
	}
}

func TestCollectGlobalScope(t *testing.T) {
	src := &fakeSource{}
	if _, err := collect(src, "o/r", account{Login: "u"}, testNow); err != nil {
		t.Fatal(err)
	}
	var global []string
	for _, q := range src.queries {
		_, query, _ := strings.Cut(q, "|")
		if strings.Contains(query, "is:pr author:u") && !strings.HasPrefix(query, "repo:o/r") {
			global = append(global, q)
		}
	}
	if len(global) != 8 {
		t.Fatalf("got %d global queries, want 8: %q", len(global), global)
	}
	for _, q := range global {
		// Own repos are excluded because anyone can merge their own PRs (#1).
		// Private repos are excluded so results don't depend on who runs the
		// tool and private repo names don't leak (#4).
		if !strings.Contains(q, " -user:u") || !strings.Contains(q, " is:public") {
			t.Errorf("global query missing scope: %q", q)
		}
	}
}

func TestCollectProjects(t *testing.T) {
	const q = "desc|is:pr is:merged is:public user:u -author:u -repo:o/r"
	src := &fakeSource{
		stars: []ownedRepo{{"u/a", 34}, {"u/b", 26}, {"u/c", 3}, {"u/d", 2}, {"u/e", 1}, {"u/f", 0}},
		search: map[string]fakeSearch{
			q: {total: 150, items: []searchItem{
				{Author: "alice"}, {Author: "Alice"}, {Author: "bob"},
				{Author: "dependabot[bot]", AuthorBot: true}, {Author: ""},
			}},
		},
	}
	m, err := collect(src, "o/r", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	g := m.Global
	// Repos under minProjectStars are ignored: a self-star and a friend on
	// every throwaway repo shouldn't add up.
	if g.ProjectStars != 63 || g.ProjectsStarred != 3 {
		t.Errorf("stars = %d on %d repos, want 63 on 3", g.ProjectStars, g.ProjectsStarred)
	}
	// Distinct people, case-insensitive, bots excluded.
	if g.Contributors != 2 || !g.ContributorsSample {
		t.Errorf("contributors = %d (sampled %v), want 2 (sampled true)", g.Contributors, g.ContributorsSample)
	}
}

func TestScoreCreditsMaintainers(t *testing.T) {
	// Works mostly in their own projects: no PRs merged in other people's
	// repos, but their projects have stars and outside contributors.
	maintainer := Metrics{Global: Global{AccountAgeDays: 1200, ProjectStars: 120, ProjectsStarred: 4, Contributors: 6}}
	// Same age, many self-merged PRs and nothing else, which no longer count.
	selfMerger := Metrics{Global: Global{AccountAgeDays: 1200}}
	m, s := elsewherePoints(score(maintainer, defaultWeights)), elsewherePoints(score(selfMerger, defaultWeights))
	if m < s+10 {
		t.Errorf("maintainer elsewhere = %.1f, self-merger = %.1f; want at least 10 more", m, s)
	}
}

func TestCollectAccountAge(t *testing.T) {
	src := &fakeSource{}
	m, err := collect(src, "o/r", account{Login: "u", CreatedAt: testNow.AddDate(0, 0, -17)}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if m.Global.AccountAgeDays != 17 {
		t.Errorf("AccountAgeDays = %d, want 17", m.Global.AccountAgeDays)
	}
}

func TestRecentTopRepoTieBreak(t *testing.T) {
	src := &fakeSource{search: map[string]fakeSearch{
		"desc|is:pr author:u is:public -user:u -repo:o/r created:>=2026-09-29": {total: 2, items: []searchItem{{Repo: "z/z"}, {Repo: "a/a"}}},
	}}
	m, _ := collect(src, "o/r", account{Login: "u"}, testNow)
	if m.Global.RecentTopRepo != "a/a" {
		t.Errorf("RecentTopRepo = %q, want a/a", m.Global.RecentTopRepo)
	}
}

func TestLandingRate(t *testing.T) {
	tests := []struct {
		name     string
		g        Global
		min, max float64
	}{
		{"no history", Global{}, 0.5, 0.5},
		{"history only", Global{PRsOpened: 8, PRsMerged: 8}, 0.9, 0.9},
		// Open PRs count as neither landed nor failed, however long they've
		// waited, because some repos take months to review.
		{"slow queue: 72 open, good history", Global{PRsOpened: 116, PRsMerged: 38, PRsOpen: 72,
			RecentPRs: 83, RecentMerged: 5, RecentClosedUnmerged: 6}, 0.78, 0.82},
		// History is capped at priorWeight PRs, so a long record cushions a
		// bad month but doesn't hide it.
		{"long record, bad month", Global{PRsOpened: 1040, PRsMerged: 1002, PRsOpen: 0,
			RecentPRs: 40, RecentMerged: 2, RecentClosedUnmerged: 38}, 0.35, 0.40},
		{"new account, mostly closed", Global{PRsOpened: 108, PRsMerged: 3, PRsOpen: 82,
			RecentPRs: 108, RecentMerged: 3, RecentClosedUnmerged: 23}, 0.14, 0.15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.g.LandingRate(); got < tt.min-1e-9 || got > tt.max+1e-9 {
				t.Errorf("LandingRate() = %.3f, want %.2f to %.2f", got, tt.min, tt.max)
			}
		})
	}
}

func TestCollectExcludesSelfUpvotes(t *testing.T) {
	src := &fakeSource{}
	if _, err := collect(src, "o/r", account{Login: "u"}, testNow); err != nil {
		t.Fatal(err)
	}
	if src.thumbsQuery != "repo:o/r author:u" || src.thumbsAuthor != "u" {
		t.Errorf("ThumbsUp(%q, %q), want the user's items without the user's own reactions", src.thumbsQuery, src.thumbsAuthor)
	}
}

func TestOthersReactions(t *testing.T) {
	tests := []struct {
		total  int
		logins []string
		want   int
	}{
		{3, []string{"a", "b", "c"}, 3},
		{3, []string{"a", "U", "c"}, 2},
		// Reactions beyond the sample can't be checked, so they count.
		{150, []string{"u"}, 149},
		{1, []string{"u"}, 0},
		{0, nil, 0},
	}
	for _, tt := range tests {
		if got := othersReactions(tt.total, tt.logins, "u"); got != tt.want {
			t.Errorf("othersReactions(%d, %v) = %d, want %d", tt.total, tt.logins, got, tt.want)
		}
	}
}

func TestMostRecentlyMerged(t *testing.T) {
	// Candidates arrive in update order. #3 is old but got a comment today,
	// and #9 was opened long ago but merged yesterday (#6).
	candidates := []rawPR{
		{Number: 3, MergedAt: day("2024-01-01")},
		{Number: 7, MergedAt: day("2026-10-20")},
		{Number: 9, MergedAt: day("2026-10-28")},
		{Number: 8, MergedAt: day("2026-10-25")},
	}
	if got := mostRecentlyMerged(candidates, 2); !slices.Equal(got, []int{9, 8}) {
		t.Errorf("mostRecentlyMerged() = %v, want [9 8]", got)
	}
	if got := mostRecentlyMerged(candidates, 10); !slices.Equal(got, []int{9, 8, 7, 3}) {
		t.Errorf("mostRecentlyMerged(10) = %v, want all four newest first", got)
	}
	if got := mostRecentlyMerged(nil, 5); got != nil {
		t.Errorf("mostRecentlyMerged(nil) = %v", got)
	}
}

func TestNotAPerson(t *testing.T) {
	for _, tt := range []struct {
		typ  string
		want string
	}{
		{"User", ""},
		{"", ""},
		{"Bot", "dependabot[bot] is a bot account."},
		{"Organization", "dependabot[bot] is an organization."},
	} {
		got := notAPerson(account{Login: "dependabot[bot]", Type: tt.typ})
		if tt.want == "" && got != "" || !strings.HasPrefix(got, tt.want) {
			t.Errorf("notAPerson(%q) = %q, want prefix %q", tt.typ, got, tt.want)
		}
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0 PRs", 1: "1 PR", 2: "2 PRs", 1500: "1,500 PRs"} {
		if got := plural(n, "PR"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestCollectBatchesSearches(t *testing.T) {
	src := &fakeSource{}
	if _, err := collect(src, "o/r", account{Login: "u"}, testNow); err != nil {
		t.Fatal(err)
	}
	// One batch for the plain searches (#26), plus one for each search with
	// extras, all sent at once. Together they can exceed GitHub's limits.
	if src.batches != 3 {
		t.Errorf("%d search batches, want 3", src.batches)
	}
	if slices.ContainsFunc(src.queries, func(q string) bool { return strings.Contains(q, "|page") }) {
		t.Errorf("paged with nothing to page: %q", src.queries)
	}
}

func TestCollectNoPRsSkipsPRQueries(t *testing.T) {
	src := &fakeSource{}
	m, err := collect(src, "o/r", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if src.mergedQuery != "" {
		t.Errorf("merged PRs fetched for a user with no PRs: %q", src.mergedQuery)
	}
	if m.FirstAuthored != nil || m.LastAuthored != nil {
		t.Errorf("authored items set with no results: %+v %+v", m.FirstAuthored, m.LastAuthored)
	}
}

func TestCollectError(t *testing.T) {
	src := &fakeSource{err: errors.New("rate limited")}
	if _, err := collect(src, "o/r", account{Login: "u"}, testNow); err == nil {
		t.Fatal("expected error")
	}
}

func TestRework(t *testing.T) {
	own := func(add, del int) rawCommit { return rawCommit{Logins: []string{"u"}, Additions: add, Deletions: del} }
	maint := func(add, del int) rawCommit { return rawCommit{Logins: []string{"m"}, Additions: add, Deletions: del} }

	tests := []struct {
		name       string
		commits    []rawCommit
		wantRework float64
		wantCredit float64
	}{
		{"untouched", []rawCommit{own(1000, 0)}, 0, 1},
		{"routine touch", []rawCommit{own(1000, 0), maint(20, 0)}, 0.02, 0.98},
		{"900 of 1000 lines replaced", []rawCommit{own(1000, 0), maint(100, 900)}, 1, 0},
		{"more rework than code", []rawCommit{own(100, 0), maint(300, 50)}, 3.5, 0},
		{"merge commits ignored", []rawCommit{own(100, 0), {Logins: []string{"m"}, Additions: 5000, Merge: true}}, 0, 1},
		{"unlinked author is the PR author", []rawCommit{{Additions: 100}, maint(10, 0)}, 0.1, 0.9},
		{"login case ignored", []rawCommit{{Logins: []string{"U"}, Additions: 100}}, 0, 1},
		{"co-authored counts as own", []rawCommit{{Logins: []string{"m", "u"}, Additions: 100}}, 0, 1},
		{"deletion-only PR", []rawCommit{own(0, 200), maint(20, 0)}, 0.1, 0.9},
		{"no commits at all", nil, 0, 1},
		{"all reverted and rewritten", []rawCommit{own(150, 0), maint(0, 150), maint(200, 0)}, 350.0 / 150, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw, credit, inferred := rework("u", rawPR{Commits: tt.commits})
			if math.Abs(rw-tt.wantRework) > 1e-9 || math.Abs(credit-tt.wantCredit) > 1e-9 {
				t.Errorf("rework() = %v, %v; want %v, %v", rw, credit, tt.wantRework, tt.wantCredit)
			}
			if inferred != nil {
				t.Errorf("inferred = %v, want nil", inferred)
			}
		})
	}
}

func TestReworkInfersContributorFromFirstCommit(t *testing.T) {
	// The PR author committed from a second account; a maintainer touched it up.
	pr := rawPR{Commits: []rawCommit{
		{Logins: []string{"u-work"}, Additions: 100},
		{Logins: []string{"u-work"}, Additions: 50},
		{Logins: []string{"m"}, Additions: 15},
	}}
	rw, credit, inferred := rework("u", pr)
	if math.Abs(rw-0.1) > 1e-9 || math.Abs(credit-0.9) > 1e-9 {
		t.Errorf("rework() = %v, %v; want 0.1, 0.9", rw, credit)
	}
	if !slices.Equal(inferred, []string{"u-work"}) {
		t.Errorf("inferred = %v, want [u-work]", inferred)
	}
}

func TestPRsClosedUnmergedNeverNegative(t *testing.T) {
	// Search counts are independent and can disagree for a moment.
	m := Metrics{PRsOpened: 2, PRsMerged: 2, PRsOpen: 1}
	if got := m.PRsClosedUnmerged(); got != 0 {
		t.Errorf("PRsClosedUnmerged() = %d, want 0", got)
	}
}

func TestScore(t *testing.T) {
	reworked := []MergedPR{{Credit: 0}, {Credit: 0}, {Credit: 0}}
	tests := []struct {
		name string
		m    Metrics
		want int
	}{
		{"nothing", Metrics{}, 0},
		// The first few count less: one merge is a thin record.
		{"one merged", Metrics{PRsOpened: 1, PRsMerged: 1}, 5},
		{"three merged", Metrics{PRsOpened: 3, PRsMerged: 3}, 12},
		{"three merged, all rewritten", Metrics{PRsOpened: 3, PRsMerged: 3, RecentMerged: reworked}, 0},
		{"one closed unmerged", Metrics{PRsOpened: 1}, 0},
		{"a few open PRs are neutral", Metrics{PRsOpened: 3, PRsOpen: 3}, 0},
		// 2 beyond the grace of 3, with nothing earned.
		{"5 open in a month, nothing else", Metrics{PRsOpened: 5, PRsOpen: 5}, -4},
		// 12 PRs in a month against 2 merged and 3 threads: well short of
		// balance.
		{"many open, some engagement", Metrics{PRsOpened: 12, PRsMerged: 2, PRsOpen: 10, Threads: 3}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := score(tt.m, defaultWeights).Score; got != tt.want {
				t.Errorf("score() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestScoreComponentsSumToScore(t *testing.T) {
	m := Metrics{PRsOpened: 40, PRsMerged: 30, Issues: 12, ThumbsUp: 50, Threads: 90, Reviews: 7,
		RecentMerged: []MergedPR{{Credit: 0.9}, {Credit: 0.4}}}
	r := score(m, defaultWeights)
	var sum float64
	for _, c := range r.Components {
		sum += c.Points
	}
	if int(math.Round(sum)) != r.Score {
		t.Errorf("components sum to %v, score is %d", sum, r.Score)
	}
}

func TestScoreGroups(t *testing.T) {
	m := Metrics{PRsOpened: 1, PRsMerged: 1, Global: Global{AccountAgeDays: 400, PRsOpened: 1, PRsMerged: 1, RecentPRs: 1}}
	r := score(m, defaultWeights)
	var groups []string
	for _, c := range r.Components {
		if !slices.Contains(groups, c.Group) {
			groups = append(groups, c.Group)
		}
	}
	if !slices.Equal(groups, []string{groupHere, groupElsewhere, groupBurden}) {
		t.Errorf("groups = %v", groups)
	}
}

func elsewherePoints(r Result) float64 {
	var sum float64
	for _, c := range r.Components {
		if c.Group == groupElsewhere {
			sum += c.Points
		}
	}
	return sum
}

func TestScoreElsewhereCapped(t *testing.T) {
	famous := Metrics{Global: Global{AccountAgeDays: 5000, PRsOpened: 6000, PRsMerged: 5000}}
	r := score(famous, defaultWeights)
	if got := elsewherePoints(r); math.Abs(got-defaultWeights.ElsewhereCap) > 1e-9 {
		t.Errorf("elsewhere = %v, want cap %v", got, defaultWeights.ElsewhereCap)
	}
	// Famous elsewhere but new here: benefit of the doubt, nothing more.
	if r.Score != 20 || r.Here != 0 || r.Elsewhere != 20 {
		t.Errorf("famous but new here: %d (here %d, elsewhere %d), want 20 from elsewhere", r.Score, r.Here, r.Elsewhere)
	}

	newbie := Metrics{Global: Global{AccountAgeDays: 30}}
	if slices.ContainsFunc(score(newbie, defaultWeights).Components, func(c Component) bool { return c.Label == "Cap" }) {
		t.Error("cap applied below the cap")
	}
}

func TestScoreNotLanding(t *testing.T) {
	notLanding := func(g Global) float64 {
		for _, c := range score(Metrics{Global: g}, defaultWeights).Components {
			if c.Label == "Recent PRs not landing" {
				return c.Points
			}
		}
		t.Fatal("no not-landing component")
		return 0
	}

	// 108 PRs in 17 days from a new account: 3 merged, 23 closed unmerged.
	spray := notLanding(Global{PRsOpened: 108, PRsMerged: 3, PRsOpen: 82, RecentPRs: 108, RecentMerged: 3, RecentClosedUnmerged: 23})
	// 60 PRs a month, 54 merged, with a long good record.
	prolific := notLanding(Global{PRsOpened: 2000, PRsMerged: 1800, RecentPRs: 60, RecentMerged: 54, RecentClosedUnmerged: 6})
	// One open PR, no history.
	first := notLanding(Global{PRsOpened: 1, PRsOpen: 1, RecentPRs: 1})
	// Community contributor in a slow repo: 83 PRs in 30 days, most still
	// waiting for review, good record before that.
	slowQueue := notLanding(Global{PRsOpened: 116, PRsMerged: 38, PRsOpen: 72, RecentPRs: 83, RecentMerged: 5, RecentClosedUnmerged: 6})
	// Long-time contributor who hasn't opened anything lately.
	dormant := notLanding(Global{PRsOpened: 300, PRsMerged: 280})

	if slowQueue < -3 {
		t.Errorf("slow-queue penalty = %.1f, want above -3", slowQueue)
	}
	if dormant != 0 {
		t.Errorf("dormant penalty = %.1f, want 0", dormant)
	}
	// Only the 26 resolved PRs count here. The 82 still open are charged
	// under authoring without engagement elsewhere.
	if spray > -20 {
		t.Errorf("spray penalty = %.1f, want below -20", spray)
	}
	if prolific < -1 {
		t.Errorf("prolific penalty = %.1f, want above -1", prolific)
	}
	if first < -2 {
		t.Errorf("first PR penalty = %.1f, want above -2", first)
	}
}

func TestScoreSprayAccountScoresBadly(t *testing.T) {
	// The pattern from a 17-day-old account that opened 106 PRs across 22 repos.
	spray := Metrics{PRsOpened: 2, Global: Global{AccountAgeDays: 17, PRsOpened: 108, PRsMerged: 3, PRsOpen: 82,
		RecentPRs: 108, RecentMerged: 3, RecentClosedUnmerged: 23}}
	// A real newcomer: established account, one closed PR here.
	newcomer := Metrics{PRsOpened: 1, Global: Global{AccountAgeDays: 1500, PRsOpened: 30, PRsMerged: 25, RecentPRs: 2}}

	s, n := score(spray, defaultWeights).Score, score(newcomer, defaultWeights).Score
	if s > -20 {
		t.Errorf("spray account scored %d, want -20 or below", s)
	}
	if n <= 0 {
		t.Errorf("newcomer scored %d, want positive", n)
	}
}

func TestScoreDormantVeteranStaysStrong(t *testing.T) {
	// Years of merged work here and elsewhere, nothing in the last month.
	dormant := Metrics{PRsOpened: 40, PRsMerged: 35, Threads: 60, Issues: 10, ThumbsUp: 30,
		Global: Global{AccountAgeDays: 3000, PRsOpened: 300, PRsMerged: 280}}
	if r := score(dormant, defaultWeights); r.Score < 80 {
		t.Errorf("dormant veteran scored %d (%s), want 80+", r.Score, r.Reason)
	}
}

func repoItems(n int, repo func(i int) string) []searchItem {
	items := make([]searchItem, n)
	for i := range items {
		items[i] = searchItem{Repo: repo(i)}
	}
	return items
}

func TestParseArgsVersion(t *testing.T) {
	got, err := parseArgs([]string{"--version"}, io.Discard)
	if err != nil || !got.version {
		t.Errorf("parseArgs(--version) = %+v, %v", got, err)
	}
}

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"--version"}, &out, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out.String(), "gh-averyindex ") {
		t.Errorf("output = %q", out.String())
	}
}

func TestBuildInfoString(t *testing.T) {
	tests := []struct {
		b    buildInfo
		want string
	}{
		{buildInfo{Version: "v0.4.0", Commit: "2754283", Date: day("2026-10-29")}, "gh-averyindex v0.4.0 (2754283, 2026-10-29)"},
		{buildInfo{Version: "dev", Commit: "2754283"}, "gh-averyindex dev (2754283)"},
		{buildInfo{Version: "dev"}, "gh-averyindex dev"},
	}
	for _, tt := range tests {
		if got := tt.b.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestVersionOverride(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v1.2.3"
	if got := currentBuild().Version; got != "v1.2.3" {
		t.Errorf("currentBuild().Version = %q, want v1.2.3", got)
	}
}

func TestRenderTextFooter(t *testing.T) {
	var buf bytes.Buffer
	m := Metrics{User: "u", Repo: "o/r"}
	if err := renderText(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if want := "averyindex v9.9.9 · 2026-10-29 12:00 UTC"; lines[len(lines)-1] != want {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], want)
	}
}

func TestRenderJSONMeta(t *testing.T) {
	var buf bytes.Buffer
	m := Metrics{User: "u", Repo: "o/r"}
	if err := renderJSON(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["schema_version"] != float64(schemaVersion) || got["version"] != "v9.9.9" ||
		got["generated_at"] != "2026-10-29T12:00:00Z" {
		t.Errorf("meta = %v %v %v", got["schema_version"], got["version"], got["generated_at"])
	}
	// The version pins the weights, so they aren't repeated in the output.
	if _, ok := got["weights"]; ok {
		t.Error("weights should not be in JSON output")
	}
}

func TestAge(t *testing.T) {
	for days, want := range map[int]string{17: "17 days", 59: "59 days", 365: "12 months", 3000: "8.2 years"} {
		if got := age(days); got != want {
			t.Errorf("age(%d) = %q, want %q", days, got, want)
		}
	}
}

func TestRateLimitWait(t *testing.T) {
	now := time.Unix(1000, 0)
	httpErr := func(status int, msg string, headers map[string]string) error {
		h := http.Header{}
		for k, v := range headers {
			h.Set(k, v)
		}
		return &api.HTTPError{StatusCode: status, Message: msg, Headers: h}
	}
	tests := []struct {
		name   string
		err    error
		want   time.Duration
		wantOK bool
	}{
		{"nil", nil, 0, false},
		{"not HTTP", errors.New("boom"), 0, false},
		{"not found", httpErr(404, "Not Found", nil), 0, false},
		{"forbidden, not rate limit", httpErr(403, "Resource not accessible", nil), 0, false},
		{"retry-after", httpErr(403, "secondary rate limit", map[string]string{"Retry-After": "30"}), 30 * time.Second, true},
		{"reset header", httpErr(403, "API rate limit exceeded", map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1045"}), 46 * time.Second, true},
		{"secondary, no headers", httpErr(403, "You have exceeded a secondary rate limit", nil), time.Minute, true},
		{"429", httpErr(429, "", map[string]string{"Retry-After": "5"}), 5 * time.Second, true},
		{"graphql rate limited", &api.GraphQLError{Errors: []api.GraphQLErrorItem{{Type: "RATE_LIMITED"}}}, time.Minute, true},
		{"graphql other error", &api.GraphQLError{Errors: []api.GraphQLErrorItem{{Type: "NOT_FOUND"}}}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rateLimitWait(tt.err, now)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("rateLimitWait() = %v, %v; want %v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestWithRetryAnnouncesWaits(t *testing.T) {
	var buf bytes.Buffer
	var slept []time.Duration
	notices, sleep = &buf, func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { notices, sleep = os.Stderr, time.Sleep })

	limited := &api.HTTPError{StatusCode: 403, Message: "secondary rate limit", Headers: http.Header{"Retry-After": {"7"}}}
	calls := 0
	err := withRetry(func() error {
		calls++
		if calls < 3 {
			return limited
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("withRetry() = %v after %d calls, want nil after 3", err, calls)
	}
	if !slices.Equal(slept, []time.Duration{7 * time.Second, 7 * time.Second}) {
		t.Errorf("slept %v", slept)
	}
	out := buf.String()
	for _, want := range []string{"attempt 1 of 3", "attempt 2 of 3", "Waiting 7s", "Not hung."} {
		if !strings.Contains(out, want) {
			t.Errorf("notices missing %q:\n%s", want, out)
		}
	}
}

func TestWithRetryGivesUpLoudly(t *testing.T) {
	var buf bytes.Buffer
	notices, sleep = &buf, func(time.Duration) {}
	t.Cleanup(func() { notices, sleep = os.Stderr, time.Sleep })

	limited := &api.HTTPError{StatusCode: 403, Message: "secondary rate limit", Headers: http.Header{"Retry-After": {"1"}}}
	if err := withRetry(func() error { return limited }); err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(buf.String(), "giving up") {
		t.Errorf("no give-up notice:\n%s", buf.String())
	}

	buf.Reset()
	long := &api.HTTPError{StatusCode: 403, Message: "rate limit", Headers: http.Header{"Retry-After": {"3600"}}}
	if err := withRetry(func() error { return long }); err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(buf.String(), "longer than the 2m0s") {
		t.Errorf("no too-long notice:\n%s", buf.String())
	}
}

func TestWithRetryRetriesServerErrors(t *testing.T) {
	// #24: GitHub times out a slow GraphQL page with a 502. It usually works
	// on the next try.
	var buf bytes.Buffer
	var slept []time.Duration
	notices, sleep = &buf, func(d time.Duration) { slept = append(slept, d) }
	t.Cleanup(func() { notices, sleep = os.Stderr, time.Sleep })

	calls := 0
	err := withRetry(func() error {
		calls++
		switch calls {
		case 1:
			return &api.HTTPError{StatusCode: http.StatusBadGateway, Message: "502 Bad Gateway"}
		case 2:
			return &api.HTTPError{StatusCode: http.StatusGatewayTimeout}
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("withRetry() = %v after %d calls, want nil after 3", err, calls)
	}
	if !slices.Equal(slept, []time.Duration{2 * time.Second, 8 * time.Second}) {
		t.Errorf("slept %v, want [2s 8s]", slept)
	}
	if !strings.Contains(buf.String(), "server error") || !strings.Contains(buf.String(), "Not hung.") {
		t.Errorf("notices:\n%s", buf.String())
	}
}

func TestTransientWait(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{&api.HTTPError{StatusCode: 502}, true},
		{&api.HTTPError{StatusCode: 503}, true},
		{&api.HTTPError{StatusCode: 504}, true},
		{&api.HTTPError{StatusCode: 500}, false},
		{&api.HTTPError{StatusCode: 404}, false},
		{errors.New("boom"), false},
		{nil, false},
	} {
		if _, ok := transientWait(tt.err, 1); ok != tt.want {
			t.Errorf("transientWait(%v) ok = %v, want %v", tt.err, ok, tt.want)
		}
	}
}

func TestRenderTextPointsAtScore(t *testing.T) {
	m := Metrics{User: "u", Repo: "o/r", PRsOpened: 3, PRsMerged: 3}
	var buf bytes.Buffer
	if err := renderText(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(buf.String(), "\n")
	if want := "u in o/r  ·  ➜ averyindex 12"; lines[0] != want {
		t.Errorf("header = %q, want %q", lines[0], want)
	}
	if want := "  Here +12  ·  Elsewhere +0  ·  Burden +0  ·  3 PRs merged here"; lines[1] != want {
		t.Errorf("parts = %q, want %q", lines[1], want)
	}
	// The final score row lines up with the component rows above it.
	var row, above string
	for i, l := range lines {
		if strings.HasPrefix(l, "➜ averyindex") {
			row, above = l, lines[i-1]
		}
	}
	if row == "" {
		t.Fatalf("no score row:\n%s", buf.String())
	}
	if utf8.RuneCountInString(row) != utf8.RuneCountInString(above) {
		t.Errorf("score row is %d columns, row above is %d:\n%s\n%s",
			utf8.RuneCountInString(row), utf8.RuneCountInString(above), above, row)
	}
}

// spinnerOutput runs a spinner until it has drawn at least frames frames,
// writes a notice, stops it, and returns everything it wrote.
func spinnerOutput(t *testing.T, sp *spinner, frames int) string {
	t.Helper()
	var buf bytes.Buffer
	sp.w = &buf
	sp.Start(time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		sp.mu.Lock()
		n := sp.frame
		sp.mu.Unlock()
		if n >= frames || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	fmt.Fprintln(sp, "GitHub rate limit hit.")
	sp.Stop()
	sp.Stop() // safe twice
	return buf.String()
}

func TestSpinner(t *testing.T) {
	out := spinnerOutput(t, newSpinner(nil, arcFrames, "Scoring u…", 80), 5)

	// Frames cycle in order, each redrawn in place with \r.
	if !strings.HasPrefix(out, "\r◜ Scoring u…\r◝ Scoring u…\r◞ Scoring u…\r◟ Scoring u…\r◜ Scoring u…") {
		t.Errorf("frames = %q", out)
	}
	// A notice erases the spinner row first, with spaces rather than an ANSI
	// code, so it works on basic terminals (Copilot on #25).
	blank := "\r" + strings.Repeat(" ", len([]rune("◜ Scoring u…"))) + "\r"
	if !strings.Contains(out, blank+"GitHub rate limit hit.\n") {
		t.Errorf("notice didn't erase the spinner row: %q", out)
	}
	if strings.Contains(out, "\033") {
		t.Errorf("escape codes in spinner output: %q", out)
	}
}

func TestSpinnerFitsNarrowTerminal(t *testing.T) {
	// Copilot on #25: a line wider than the terminal wraps, and \r can't get
	// back to the rows above.
	label := "Scoring a-very-long-login in some-organization/some-repository-name…"
	out := spinnerOutput(t, newSpinner(nil, arcFrames, label, 30), 2)
	for part := range strings.SplitSeq(strings.ReplaceAll(out, "\n", "\r"), "\r") {
		if n := utf8.RuneCountInString(part); n > 29 {
			t.Errorf("drew %d columns on a 30-column terminal: %q", n, part)
		}
	}
	if !strings.Contains(out, "◜ Scoring a-very-long-login …") {
		t.Errorf("label not cut with an ellipsis: %q", out)
	}
}

func TestSpinnerDefaultWidth(t *testing.T) {
	if sp := newSpinner(nil, arcFrames, "x", 0); sp.width != defaultWidth {
		t.Errorf("width = %d, want %d when unknown", sp.width, defaultWidth)
	}
}

func TestSpinnerFastRunPrintsNothing(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, arcFrames, "x", 80)
	sp.Start(time.Hour)
	sp.Stop()
	if buf.Len() != 0 {
		t.Errorf("output = %q, want nothing", buf.String())
	}
}

func TestSpinnerFrames(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	if got := spinnerFrames(); !slices.Equal(got, arcFrames) {
		t.Errorf("xterm frames = %v", got)
	}
	t.Setenv("TERM", "dumb")
	if got := spinnerFrames(); !slices.Equal(got, asciiFrames) {
		t.Errorf("TERM=dumb frames = %v, want ASCII", got)
	}
}

func TestRenderTextHighlightsScore(t *testing.T) {
	m := Metrics{User: "u", Repo: "o/r", PRsOpened: 3, PRsMerged: 3}
	r := score(m, defaultWeights)

	var plain, color bytes.Buffer
	if err := renderText(&plain, m, r, testMeta); err != nil {
		t.Fatal(err)
	}
	md := testMeta
	md.Color = true
	if err := renderText(&color, m, r, md); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(plain.String(), "\033[") {
		t.Errorf("escape codes without color:\n%q", plain.String())
	}
	// The header score and the final score row are highlighted, and nothing
	// else changes.
	header := "u in o/r  ·  \033[1m➜ averyindex 12\033[0m\n"
	if !strings.HasPrefix(color.String(), header) {
		t.Errorf("header = %q", strings.SplitN(color.String(), "\n", 2)[0])
	}
	if got := strings.Count(color.String(), "\033[1m"); got != 2 {
		t.Errorf("%d highlights, want 2", got)
	}
	stripped := regexp.MustCompile("\033\\[[0-9;]*m").ReplaceAllString(color.String(), "")
	if stripped != plain.String() {
		t.Errorf("color output differs from plain beyond escape codes")
	}
}

func TestStartProgressOnlyOnTerminal(t *testing.T) {
	var buf bytes.Buffer
	stop := startProgress(&buf, "x")
	stop()
	if notices != os.Stderr || buf.Len() != 0 {
		t.Errorf("progress started on a non-terminal writer")
	}
}

func TestRetryNotice(t *testing.T) {
	got := retryNotice("GitHub rate limit hit.", time.Minute, 1, time.Date(2026, 10, 29, 9, 40, 0, 0, time.UTC))
	want := "GitHub rate limit hit. Waiting 1m0s (until 09:41:00) before retrying, attempt 1 of 3. Not hung."
	if got != want {
		t.Errorf("retryNotice() =\n%q\nwant\n%q", got, want)
	}
}

func TestScoreHelpfulBeatsDriveBy(t *testing.T) {
	regular := Metrics{PRsOpened: 5, PRsMerged: 4, Issues: 3, Threads: 15, ThumbsUp: 10}
	driveBy := Metrics{PRsOpened: 30, PRsMerged: 1}
	if r, d := score(regular, defaultWeights).Score, score(driveBy, defaultWeights).Score; r <= d {
		t.Errorf("regular contributor scored %d, drive-by scored %d", r, d)
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    options
		wantErr bool
	}{
		{"user only", []string{"YakDriver"}, options{user: "YakDriver"}, false},
		{"at prefix", []string{"@YakDriver"}, options{user: "YakDriver"}, false},
		{"flags first", []string{"-R", "o/r", "--json", "u"}, options{repo: "o/r", json: true, user: "u"}, false},
		{"flags after user", []string{"u", "--repo", "o/r"}, options{repo: "o/r", user: "u"}, false},
		{"no user", []string{"--json"}, options{}, true},
		{"explain", []string{"--explain", "u"}, options{explain: true, user: "u"}, false},
		{"explain and json", []string{"--explain", "--json", "u"}, options{}, true},
		{"pr", []string{"--pr", "50687"}, options{number: 50687}, false},
		{"pr with repo and json", []string{"-R", "o/r", "--json", "--pr", "7"}, options{repo: "o/r", json: true, number: 7}, false},
		{"pr and user", []string{"--pr", "7", "u"}, options{}, true},
		{"negative pr", []string{"--pr", "-1"}, options{}, true},
		{"pr URL", []string{"https://github.com/hashicorp/terraform-provider-aws/pull/50687"},
			options{repo: "github.com/hashicorp/terraform-provider-aws", number: 50687}, false},
		{"issue URL with comment anchor", []string{"https://github.com/o/r/issues/12#issuecomment-99"},
			options{repo: "github.com/o/r", number: 12}, false},
		{"PR files URL", []string{"https://github.com/o/r/pull/12/files"}, options{repo: "github.com/o/r", number: 12}, false},
		{"enterprise URL", []string{"https://ghe.example.com/o/r/pull/3"}, options{repo: "ghe.example.com/o/r", number: 3}, false},
		{"URL with matching -R", []string{"-R", "O/R", "https://github.com/o/r/pull/3"}, options{repo: "github.com/o/r", number: 3}, false},
		// Copilot on #22: same repository spelled with its host.
		{"URL with host-qualified -R", []string{"-R", "github.com/o/r", "https://github.com/o/r/pull/7"},
			options{repo: "github.com/o/r", number: 7}, false},
		{"URL with -R on another host", []string{"-R", "ghe.example.com/o/r", "https://github.com/o/r/pull/7"}, options{}, true},
		// Copilot on #22: numbers are positive decimals, never octal.
		{"pr with leading zero", []string{"--pr", "010"}, options{}, true},
		{"pr zero with user", []string{"--pr", "0", "alice"}, options{}, true},
		{"pr not a number", []string{"--pr", "abc"}, options{}, true},
		{"URL number zero", []string{"https://github.com/o/r/pull/0"}, options{}, true},
		{"URL number overflow", []string{"https://github.com/o/r/pull/99999999999999999999"}, options{}, true},
		{"URL with other -R", []string{"-R", "x/y", "https://github.com/o/r/pull/3"}, options{}, true},
		{"repo URL", []string{"https://github.com/o/r"}, options{}, true},
		{"two users", []string{"a", "b"}, options{}, true},
		{"empty user", []string{"@"}, options{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args, io.Discard)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseArgs() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseArgsURLIgnoresDefaultHost(t *testing.T) {
	// Copilot on #22: with GH_HOST pointing at Enterprise, a github.com URL
	// must still mean github.com.
	t.Setenv("GH_HOST", "ghe.example.com")
	got, err := parseArgs([]string{"https://github.com/o/r/pull/7"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	r, err := resolveRepo(got.repo)
	if err != nil || r.Host != "github.com" {
		t.Errorf("resolved %+v, %v; want host github.com", r, err)
	}
	if _, err := parseArgs([]string{"-R", "o/r", "https://github.com/o/r/pull/7"}, io.Discard); err == nil {
		t.Error("-R o/r resolves to ghe.example.com here, so it should conflict with a github.com URL")
	}
}

func TestParseArgsHelp(t *testing.T) {
	_, err := parseArgs([]string{"-h"}, io.Discard)
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("parseArgs(-h) error = %v, want flag.ErrHelp", err)
	}
}

func TestRenderText(t *testing.T) {
	m := Metrics{
		User: "u", Repo: "o/r",
		PRsOpened: 1250, PRsMerged: 1200, PRsOpen: 12, ThumbsUp: 3, ThumbsUpTruncated: true,
		Issues:        2,
		FirstAuthored: &searchItem{Number: 7, CreatedAt: day("2019-05-01")},
		LastAuthored:  &searchItem{Number: 70, CreatedAt: day("2026-10-01"), IsPR: true},
		RecentMerged: []MergedPR{
			{Number: 70, MergedAt: day("2026-10-02"), Additions: 1500, Deletions: 30, Rework: 0.12, Credit: 0.88, CommitsBy: []string{"u-work"}, CommitsTruncated: true},
		},
		OpenPRs: []searchItem{{Number: 71, CreatedAt: day("2026-10-20"), IsPR: true}},
		Global: Global{AccountCreated: day("2024-10-29"), AccountAgeDays: 730,
			PRsOpened: 1300, PRsMerged: 1250, PRsOpen: 12,
			RecentPRs: 150, RecentRepos: 2, RecentTopRepo: "o/r", RecentTopCount: 70, RecentSampled: 100},
	}
	var buf bytes.Buffer
	if err := renderText(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"u in o/r  ·  ",
		"(first 1,000 items)",
		"issue #7, 2019-05-01",
		"PR #70, 2026-10-01",
		"1,200 merged · 12 open · 38 closed unmerged",
		"#70  merged 2026-10-02  +1,500 -30  rework  12%  credit 0.88  (commits by u-work)  (first 100 commits)",
		"#71  open since 2026-10-20",
		"and 11 more open",
		"Rework checked on the 1 most recent merged PRs.",
		"Merged PRs (1,200 merged × 0.88)",
		"Elsewhere on GitHub",
		"averyindex",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, want := range []string{
		`Account created +2024-10-29 \(2\.0 years ago\)`,
		`PRs opened +1,300 · 1,250 merged · 12 open · 38 closed unmerged`,
		`Last 30 days +150 PRs · 0 merged · 0 closed unmerged · 150 open`,
		`\n +across 2 repos \(most: 70 in o/r\), repos from the newest 100`,
		`Elsewhere [+-]\d+ \(gains \+20\.0 of 20, costs -[\d.]+\)`,
		`Cap \(\+39\.6 of gains counts as \+20\) +-19\.6`,
		`Own projects +0 stars on 0 repos with 3\+ · 0 contributors`,
	} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("output doesn't match %q:\n%s", want, out)
		}
	}
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimRight(line, " ") != line {
			t.Errorf("line %d has trailing spaces: %q", i+1, line)
		}
	}
	if strings.Contains(out, "Drive-by") {
		t.Errorf("drive-by shown for a participating user:\n%s", out)
	}
}

func TestRenderTextNoActivity(t *testing.T) {
	var buf bytes.Buffer
	m := Metrics{User: "u", Repo: "o/r"}
	if err := renderText(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !regexp.MustCompile(`First authored +never`).MatchString(out) {
		t.Errorf("missing 'First authored never':\n%s", out)
	}
	if !strings.Contains(out, "Pull requests  none") {
		t.Errorf("missing 'Pull requests  none':\n%s", out)
	}
	if strings.Contains(out, "-0.0") {
		t.Errorf("negative zero in output:\n%s", out)
	}
}

func TestRenderJSON(t *testing.T) {
	m := Metrics{User: "u", Repo: "o/r", PRsOpened: 3, PRsMerged: 3}
	var buf bytes.Buffer
	if err := renderJSON(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if got["user"] != "u" || got["averyindex"] != float64(12) || got["here"] != float64(12) ||
		got["burden"] != float64(0) || got["reason"] != "3 PRs merged here" {
		t.Errorf("unexpected JSON: %s", buf.String())
	}
	for _, gone := range []string{"tier", "flags"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%s is gone in schema 4: %s", gone, buf.String())
		}
	}
	if _, ok := got["first_authored"]; ok {
		t.Errorf("nil first_authored should be omitted: %s", buf.String())
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSprayPoints(t *testing.T) {
	for _, tt := range []struct {
		repos  int
		lo, hi float64
	}{
		// Reviewer on #30: at -25, heavy spray outweighs the +20 Elsewhere cap.
		{0, 0, 0}, {1, -0.1, 0}, {5, -0.5, -0.3}, {11, -2.4, -2.0}, {15, -5.6, -5.2},
		{20, -12.5, -12.1}, {30, -23.1, -22.8}, {50, -24.9, -24.7}, {500, -24.9, -24.7},
	} {
		if got := sprayPoints(tt.repos, defaultWeights); got < tt.lo || got > tt.hi {
			t.Errorf("sprayPoints(%d) = %.2f, want %.1f to %.1f", tt.repos, got, tt.lo, tt.hi)
		}
	}
}

func TestSprayRepos(t *testing.T) {
	items := []searchItem{
		{Repo: "a/a", State: "CLOSED"}, {Repo: "a/a", State: "CLOSED"}, // counts once
		{Repo: "b/b", State: "CLOSED"}, {Repo: "b/b", State: "MERGED"}, // something landed
		{Repo: "c/c", State: "OPEN"}, // waiting for review doesn't count
		{Repo: "d/d", State: "OPEN"}, {Repo: "d/d", State: "CLOSED"},
	}
	if got := sprayRepos(items); got != 2 {
		t.Errorf("sprayRepos() = %d, want 2 (a/a and d/d)", got)
	}
}

func TestCollectSprayWindowSkipsRecent(t *testing.T) {
	// Spray starts the day before the 30-day window, so not-landing and
	// spray never count the same PR (tuning.md, H2e).
	const q = "is:pr author:u is:public -user:u -repo:o/r created:2026-05-02..2026-09-28"
	src := &fakeSource{search: map[string]fakeSearch{"desc|" + q: {total: 3, items: []searchItem{
		{Repo: "a/a", State: "CLOSED"}, {Repo: "b/b", State: "CLOSED"}, {Repo: "c/c", State: "MERGED"},
	}}}}
	m, err := collect(src, "o/r", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if m.Global.SprayRepos != 2 || m.Global.SpraySampled != 3 {
		t.Errorf("spray = %d repos from %d PRs, want 2 from 3", m.Global.SprayRepos, m.Global.SpraySampled)
	}
	if !slices.Contains(src.details, q) {
		t.Errorf("spray search isn't detailed: %q", src.details)
	}
}

func TestAcceptedByOthers(t *testing.T) {
	items := []searchItem{
		{MergedBy: "U"},               // self-merged, no approval
		{MergedBy: "u", Approvals: 1}, // self-merged after approval
		{MergedBy: "maintainer"},      // merged by someone else
		{MergedBy: ""},                // merger's account deleted
		{MergedBy: "kubernetes-prow"}, // merged by a bot after /lgtm and /approve labels
	}
	if got := acceptedByOthers("u", items); got != 4 {
		t.Errorf("acceptedByOthers() = %d, want 4", got)
	}
}

func TestScoreElsewhereWeightedByAcceptance(t *testing.T) {
	base := Global{AccountAgeDays: 30, PRsMerged: 100}
	merged := func(g Global) Component {
		r := score(Metrics{Global: g}, defaultWeights)
		return r.Components[slices.IndexFunc(r.Components, func(c Component) bool { return c.Label == "PRs merged elsewhere" })]
	}
	full := merged(base)
	if full.Count != 100 || full.Detail != "" {
		t.Errorf("no sample: %+v", full)
	}
	base.AcceptedElsewhere, base.AcceptedSampled = 25, 100
	quarter := merged(base)
	if quarter.Count != 25 || quarter.Points >= full.Points ||
		quarter.Detail != "100 merged · 25% accepted by others" {
		t.Errorf("quarter accepted: %+v (full %+v)", quarter, full)
	}
}

func TestMaintainerCommits(t *testing.T) {
	c := func(logins ...string) rawCommit { return rawCommit{Logins: logins, Additions: 1} }
	pr := rawPR{Commits: []rawCommit{
		c("u"), c(), // own, and unlinked counts as own
		c("m1"), c("m2"), c("m1", "u"), // co-authored with u counts as own
		c("github-actions[bot]"),
		{Logins: []string{"m1"}, Merge: true},
	}}
	if got := maintainerCommits("u", pr); got != 2 {
		t.Errorf("maintainerCommits() = %d, want 2", got)
	}
	// G1's case: a long review, but the author made every commit.
	own := rawPR{Commits: slices.Repeat([]rawCommit{c("u")}, 63)}
	if got := maintainerCommits("u", own); got != 0 {
		t.Errorf("own commits = %d, want 0", got)
	}
}

func TestCollectHeavyPRs(t *testing.T) {
	c := func(login string, n int) []rawCommit {
		return slices.Repeat([]rawCommit{{Logins: []string{login}, Additions: 1}}, n)
	}
	src := &fakeSource{merged: 14, prs: []rawPR{{Number: 5, Commits: c("u", 1)}}, reviewed: []rawPR{
		{Number: 43, ReviewThreads: 28, Commits: append(c("u", 5), c("m", 10)...)}, // heavy
		{Number: 44, ReviewThreads: 21, Commits: append(c("u", 60), c("m", 9)...)}, // one short
	}}
	src.search = map[string]fakeSearch{"|repo:o/r is:pr author:u": {total: 20}}
	m, err := collect(src, "o/r", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if want := []HeavyPR{{Number: 43, ReviewThreads: 28, MaintainerCommits: 10}}; !slices.Equal(m.HeavyPRs, want) {
		t.Errorf("heavy = %+v, want %+v", m.HeavyPRs, want)
	}
	if src.reviewedQuery != "o/r repo:o/r is:pr author:u is:merged >=20" {
		t.Errorf("reviewed query = %q", src.reviewedQuery)
	}
	if got := m.CodeCredit(); got != 13 {
		t.Errorf("code credit = %v, want 13 (14 merged, 1 heavy)", got)
	}
}

func TestAvgCreditSkipsHeavy(t *testing.T) {
	m := Metrics{PRsMerged: 10, RecentMerged: []MergedPR{{Number: 1, Credit: 0}, {Number: 2, Credit: 0.5}},
		HeavyPRs: []HeavyPR{{Number: 1}}}
	// PR 1 already earns nothing as a heavy PR, so its rework isn't counted
	// again in the average.
	if got := m.AvgCredit(); got != 0.5 {
		t.Errorf("AvgCredit() = %v, want 0.5", got)
	}
	m.HeavyPRs = []HeavyPR{{Number: 1}, {Number: 2}}
	if got := m.AvgCredit(); got != 1 {
		t.Errorf("all sampled PRs heavy: AvgCredit() = %v, want 1", got)
	}
}

func TestCollectHeavyCheckFailureKeepsReport(t *testing.T) {
	var buf bytes.Buffer
	old := notices
	notices = &buf
	defer func() { notices = old }()
	src := &fakeSource{merged: 3, prs: []rawPR{{Number: 5}}, reviewedErr: errors.New("boom")}
	src.search = map[string]fakeSearch{"|repo:o/r is:pr author:u": {total: 3}}
	m, err := collect(src, "o/r", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatalf("a failed heavy check shouldn't fail the run: %v", err)
	}
	if !m.HeavyUnchecked || m.PRsMerged != 3 || len(m.RecentMerged) != 1 {
		t.Errorf("metrics = %+v", m)
	}
	if !strings.Contains(buf.String(), "heavy maintainer involvement: boom") {
		t.Errorf("notice = %q", buf.String())
	}
	var out bytes.Buffer
	if err := renderText(&out, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Heavy maintainer involvement wasn't checked") {
		t.Errorf("report doesn't say the check is missing:\n%s", out.String())
	}
}

func TestRenderHeavyPRsOnce(t *testing.T) {
	// Copilot on #28: a heavy PR in the recent sample was listed twice, once
	// with its line-rework credit.
	m := Metrics{User: "u", Repo: "o/r", PRsOpened: 2, PRsMerged: 2,
		RecentMerged: []MergedPR{{Number: 7, MergedAt: testNow, Rework: 0.07, Credit: 0.93}},
		HeavyPRs: []HeavyPR{{Number: 7, ReviewThreads: 136, MaintainerCommits: 18},
			{Number: 3, ReviewThreads: 21, MaintainerCommits: 12, CommitsTruncated: true}}}
	var out bytes.Buffer
	if err := renderText(&out, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Count(s, "#7 ") != 1 || strings.Contains(s, "credit 0.93") ||
		!strings.Contains(s, "credit 0  (heavy: 136 review threads, 18 commits by others)") {
		t.Errorf("heavy PR in the sample:\n%s", s)
	}
	if !strings.Contains(s, "(heavy: 21 review threads, 12 commits by others in the last 100)") {
		t.Errorf("heavy PR outside the sample:\n%s", s)
	}
}

func TestAuthoringPointsRate(t *testing.T) {
	// Authoring only, 6 PRs and 1 merge. In a month that's a burst. Over two
	// years it's an ordinary occasional contributor.
	burst := authoringPoints(6, 1, 0, 6, 1, defaultWeights)
	slow := authoringPoints(6, 1, 0, 6.0/24, 1, defaultWeights)
	if burst > -3 || slow < -0.2 {
		t.Errorf("burst %.1f, slow %.1f", burst, slow)
	}
	// A1: 41 PRs in about a month costs the full amount.
	if full, a1 := authoringPoints(41, 1, 0, 410, 1, defaultWeights), authoringPoints(41, 1, 0, 41/1.1, 1, defaultWeights); full != a1 {
		t.Errorf("A1 at 37 a month should pay in full: %.1f vs %.1f", a1, full)
	}
}

func TestAuthoringPoints(t *testing.T) {
	w := defaultWeights
	for _, tt := range []struct {
		name              string
		prs, merged, gave int
		lo, hi            float64
	}{
		{"a few PRs", 3, 0, 0, 0, 0},
		// An AI PR factory: A1's numbers.
		{"many PRs, one merged, nothing else", 41, 1, 0, -26, -24},
		{"one merge, a few waiting, in a month", 7, 1, 0, -6.2, -5.8},
		// YakDriver: 1,797 PRs, 101 closed, but 1,686 merged and 2,040
		// issues, comments, and reviews.
		{"authors and reviews", 1797, 1686, 2040, 0, 0},
		{"merges alone balance", 30, 30, 0, 0, 0},
		// An issue, comment, or review counts a quarter of a merge.
		{"engagement alone balances at 4 per PR", 20, 0, 80, 0, 0},
		// Reviewer on #30: 40 "claiming this" comments are cheap.
		{"A1 with 40 comments", 41, 1, 40, -14.5, -13.5},
		{"about half short", 19, 5, 20, -4.8, -4.4},
	} {
		if got := authoringPoints(tt.prs, tt.merged, tt.gave, float64(tt.prs), 1, w); got < tt.lo || got > tt.hi {
			t.Errorf("%s: authoringPoints(%d, %d, %d) = %.1f, want %.1f to %.1f", tt.name, tt.prs, tt.merged, tt.gave, got, tt.lo, tt.hi)
		}
	}
}

func TestCollectElsewhereEngagement(t *testing.T) {
	at := testNow.AddDate(0, 0, -5)
	old := testNow.AddDate(0, 0, -40)
	src := &fakeSource{
		comments: []engaged{
			{Repo: "a/b", Owner: "a", Number: 1, Author: "x", At: at},
			{Repo: "a/b", Owner: "a", Number: 1, Author: "x", At: at},  // same thread twice
			{Repo: "a/b", Owner: "a", Number: 2, Author: "u", At: at},  // own thread
			{Repo: "a/b", Owner: "a", Number: 3, Author: "x", At: old}, // before the window
			{Repo: "o/r", Owner: "o", Number: 4, Author: "x", At: at},  // the scored repo
			{Repo: "u/p", Owner: "u", Number: 5, Author: "x", At: at},  // own repo
			{Repo: "c/d", Owner: "c", Number: 6, Author: "x", At: at, Private: true},
			{Repo: "e/f", Owner: "e", Number: 7, At: at}, // author's account deleted
		},
		reviews: []engaged{
			{Repo: "a/b", Owner: "a", Number: 9, Author: "x", At: at},
			{Repo: "o/r", Owner: "o", Number: 10, At: at},
			// Copilot on #30: commented on and reviewed counts once, as a
			// review.
			{Repo: "a/b", Owner: "a", Number: 1, Author: "x", At: at},
			// Review on their own PR.
			{Repo: "g/h", Owner: "g", Number: 11, Author: "u", At: at},
		},
	}
	m, err := collect(src, "o/r", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if m.Global.RecentThreads != 1 || m.Global.RecentReviews != 2 {
		t.Errorf("threads %d, reviews %d, want 1 and 2", m.Global.RecentThreads, m.Global.RecentReviews)
	}
	if !src.engagedSince.Equal(testNow.AddDate(0, 0, -recentDays)) {
		t.Errorf("engagement since %v", src.engagedSince)
	}
	if want := "|is:issue author:u is:public -user:u -repo:o/r created:>=2026-09-29"; !slices.Contains(src.queries, want) {
		t.Errorf("missing query %q", want)
	}
	// Copilot on #30: elsewhere leaves out the scored repo.
	for _, q := range src.queries {
		if strings.Contains(q, "-user:u") && !strings.Contains(q, "-repo:o/r") {
			t.Errorf("elsewhere query includes the scored repo: %q", q)
		}
	}
}

func TestCollectEngagementFailureKeepsReport(t *testing.T) {
	src := &fakeSource{engageErr: errors.New("timeout")}
	var buf bytes.Buffer
	old := notices
	notices = &buf
	t.Cleanup(func() { notices = old })
	m, err := collect(src, "o/r", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Global.EngagementUnchecked || !strings.Contains(buf.String(), "comments and reviews elsewhere") {
		t.Errorf("unchecked %v, notice %q", m.Global.EngagementUnchecked, buf.String())
	}
	// Missing engagement isn't charged as none.
	m.Global.RecentPRs = 40
	for _, c := range score(m, defaultWeights).Components {
		if c.Label == "Authoring without engagement elsewhere" && c.Points != 0 {
			t.Errorf("unchecked engagement cost %.1f", c.Points)
		}
	}
}

// Carried over from TestFlags, which guarded these cases before flags were
// removed.
func TestScoreCarriedFromFlags(t *testing.T) {
	// An established account with one closed PR here and two comments: no
	// cost, and the reason isn't a cost.
	honest := score(Metrics{PRsOpened: 1, Threads: 2,
		Global: Global{AccountAgeDays: 1500, PRsOpened: 1, RecentPRs: 1, RecentClosedUnmerged: 1}}, defaultWeights)
	if honest.Burden != 0 || honest.Elsewhere < 0 || honest.Score <= 0 || honest.Reason != "account 4.1 years old" {
		t.Errorf("honest newcomer: %+v", honest)
	}
	// A slow review queue here: 10 PRs over a year, 3 merged, 5 comments.
	// Not a factory, so Burden is about 0.
	slow := score(Metrics{PRsOpened: 10, PRsMerged: 3, PRsOpen: 7, Threads: 5, ActiveDays: 400, RecentPRs: 3,
		Global: Global{AccountAgeDays: 1487}}, defaultWeights)
	if slow.Burden < -2 {
		t.Errorf("slow review queue: Burden %d", slow.Burden)
	}
}

func TestAuthoringRateUsesRecentPRs(t *testing.T) {
	// Reviewer on #30: one issue three years ago doesn't shield 40 PRs in
	// the last 90 days.
	burst := Metrics{PRsOpened: 41, PRsMerged: 1, ActiveDays: 30, RecentPRs: 41}
	shielded := burst
	shielded.ActiveDays = 1095
	if a, b := score(burst, defaultWeights).Burden, score(shielded, defaultWeights).Burden; a != b || a > -20 {
		t.Errorf("Burden %d in a month, %d after an old issue", a, b)
	}
}

func TestNotLandingAndAuthoringSplitPRs(t *testing.T) {
	// Reviewer on #30: the same recent PRs aren't charged twice. Not
	// landing counts the resolved ones, and authoring the open ones.
	g := Global{AccountAgeDays: 1000, PRsOpened: 155, PRsMerged: 32, PRsOpen: 76,
		RecentPRs: 155, RecentMerged: 32, RecentClosedUnmerged: 47}
	r := score(Metrics{Global: g}, defaultWeights)
	for _, c := range r.Components {
		switch c.Label {
		case "Recent PRs not landing":
			if c.Count != 79 {
				t.Errorf("not landing counts %v PRs, want the 79 resolved", c.Count)
			}
		case "Authoring without engagement elsewhere":
			if c.Count != 76 {
				t.Errorf("authoring counts %v PRs, want the 76 open", c.Count)
			}
		}
	}
}

func TestScoreTruncatedEngagementSaysSo(t *testing.T) {
	// Copilot on #30: a lower bound on engagement reads as one.
	g := Global{AccountAgeDays: 1000, RecentPRs: 40, RecentReviews: 2, EngagementTruncated: true}
	r := score(Metrics{Global: g}, defaultWeights)
	if r.Reason != "40 PRs elsewhere in 30 days still open, at least 2 issues, comments, and reviews" {
		t.Errorf("reason = %q", r.Reason)
	}
	for _, c := range r.Components {
		if c.Label == "Authoring without engagement elsewhere" && (c.Points >= 0 || !strings.Contains(c.Detail, "at least 2 given")) {
			t.Errorf("%+v", c)
		}
	}
}

func TestCollectProjectsLeavesOutScoredRepo(t *testing.T) {
	// Copilot on #30: scoring u/a, which u owns, doesn't earn Elsewhere
	// points from u/a.
	src := &fakeSource{stars: []ownedRepo{{"u/a", 500}, {"u/b", 10}}}
	m, err := collect(src, "u/a", account{Login: "u"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if m.Global.ProjectStars != 10 || m.Global.ProjectsStarred != 1 {
		t.Errorf("stars %d on %d repos, want 10 on 1", m.Global.ProjectStars, m.Global.ProjectsStarred)
	}
	if want := "desc|is:pr is:merged is:public user:u -author:u -repo:u/a"; !slices.Contains(src.queries, want) {
		t.Errorf("contributor search doesn't leave out the scored repo: %q", src.queries)
	}
}

func TestReason(t *testing.T) {
	for _, tt := range []struct {
		name string
		m    Metrics
		want string
	}{
		{"nothing", Metrics{}, "no activity here or elsewhere"},
		{"merged work", Metrics{PRsOpened: 3, PRsMerged: 3}, "3 PRs merged here"},
		// Reviewer on #30: a drive-by is explained by the PRs, not the
		// account's age.
		{"drive-by", Metrics{PRsOpened: 3, Global: Global{AccountAgeDays: 1790}},
			"3 PRs here, none merged, no issues, comments, or reviews"},
		// Reviewer on #30: a self-merge farm shows the share others accepted.
		{"self-merged elsewhere", Metrics{Global: Global{AccountAgeDays: 30, PRsMerged: 1645, AcceptedSampled: 100, AcceptedElsewhere: 21}},
			"1,645 PRs merged elsewhere, 21% accepted by others"},
		{"asking far beyond earned", Metrics{PRsOpened: 41, PRsMerged: 1, PRsOpen: 37, ThumbsUp: 2},
			"41 PRs here, 37 open, 1 merged, no issues, comments, or reviews"},
		// Reviewer on #30: the reason states the pace the cost is about.
		{"pace", Metrics{PRsOpened: 41, PRsMerged: 1, PRsOpen: 37, ActiveDays: 36, RecentPRs: 35,
			FirstAuthored: &searchItem{Number: 1, IsPR: true}},
			"41 PRs here in 5 weeks, 37 open, 1 merged, no issues, comments, or reviews"},
		{"recent burst after an old issue", Metrics{PRsOpened: 41, PRsMerged: 1, PRsOpen: 37, ActiveDays: 1100, RecentPRs: 38,
			FirstAuthored: &searchItem{Number: 1}},
			"41 PRs here, 38 in 30 days, 37 open, 1 merged, no issues, comments, or reviews"},
		// A cost that's small next to what was earned isn't the story.
		{"veteran with closed PRs", Metrics{PRsOpened: 1800, PRsMerged: 1686, PRsOpen: 10, Reviews: 1117},
			"1,686 PRs merged here"},
		{"heavy PR", Metrics{PRsOpened: 1, PRsMerged: 1, HeavyPRs: []HeavyPR{{Number: 1}}},
			"1 merged PR here needed heavy maintainer work"},
		{"spray elsewhere", Metrics{Global: Global{AccountAgeDays: 2000, SprayRepos: 30, SpraySampled: 100}},
			"PRs closed unmerged in 30 repos elsewhere"},
	} {
		if got := score(tt.m, defaultWeights).Reason; got != tt.want {
			t.Errorf("%s: reason = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestRenderSummaryIsTwoLines(t *testing.T) {
	m := Metrics{User: "u", Repo: "o/r", PRsOpened: 41, PRsMerged: 1, PRsOpen: 37}
	var buf bytes.Buffer
	if err := renderSummary(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	want := "u in o/r  ·  ➜ averyindex -45\n" +
		"  Here +5  ·  Elsewhere +0  ·  Burden -50  ·  41 PRs here, 37 open, 1 merged, no issues, comments, or reviews\n"
	if buf.String() != want {
		t.Errorf("summary:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestRenderHint(t *testing.T) {
	for _, tt := range []struct {
		name string
		md   meta
		want string
	}{
		{"piped", meta{}, ""},
		{"terminal, no color", meta{Terminal: true}, "  --explain for the full report\n"},
		{"terminal, color", meta{Terminal: true, Color: true}, "  \033[2m--explain for the full report\033[0m\n"},
	} {
		var buf bytes.Buffer
		if err := renderHint(&buf, tt.md); err != nil {
			t.Fatal(err)
		}
		if buf.String() != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, buf.String(), tt.want)
		}
	}
}

func TestScorePartsAddUp(t *testing.T) {
	m := Metrics{PRsOpened: 41, PRsMerged: 1, PRsOpen: 37, ThumbsUp: 2,
		Global: Global{AccountAgeDays: 1500, PRsMerged: 37, RecentPRs: 69, RecentMerged: 1, RecentClosedUnmerged: 4}}
	r := score(m, defaultWeights)
	if r.Score != r.Here+r.Elsewhere+r.Burden {
		t.Errorf("score %d != here %d + elsewhere %d + burden %d", r.Score, r.Here, r.Elsewhere, r.Burden)
	}
	if r.Burden > 0 {
		t.Errorf("burden is positive: %d", r.Burden)
	}
}

func TestReasonUsesCappedElsewhere(t *testing.T) {
	// Copilot on #30: 5,000 PRs merged elsewhere add 20 after the cap, less
	// than 3 merged here.
	m := Metrics{PRsOpened: 3, PRsMerged: 3, Global: Global{AccountAgeDays: 3000}}
	if got := score(m, defaultWeights).Reason; got != "3 PRs merged here" {
		t.Errorf("reason = %q", got)
	}
}
