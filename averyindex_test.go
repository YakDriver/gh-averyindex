package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

type fakeSearch struct {
	total int
	items []searchItem
}

type fakeSource struct {
	search      map[string]fakeSearch // keyed by order + "|" + query
	queries     []string
	merged      int
	prs         []rawPR
	mergedQuery string
	thumbs      int
	truncated   bool
	stars       []int
	err         error
}

func (f *fakeSource) SearchPage(q, order string, page int) (int, []searchItem, error) {
	key := fmt.Sprintf("%s|%s|page%d", order, q, page)
	f.queries = append(f.queries, key)
	r := f.search[key]
	return r.total, r.items, f.err
}

func (f *fakeSource) OwnedRepoStars(string) ([]int, error) {
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

func (f *fakeSource) MergedPRs(q string, n int) (int, []rawPR, error) {
	f.mergedQuery = q
	return f.merged, f.prs[:min(n, len(f.prs))], nil
}

func (f *fakeSource) ThumbsUp(string) (int, bool, error) {
	return f.thumbs, f.truncated, nil
}

var testNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

var testMeta = meta{Version: "v9.9.9", GeneratedAt: testNow, Weights: defaultWeights}

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCollect(t *testing.T) {
	const r = "repo:o/r"
	const g = "is:pr author:u is:public -user:u"
	first := searchItem{Number: 1, CreatedAt: day("2020-01-01")}
	last := searchItem{Number: 9, CreatedAt: day("2026-10-01"), IsPR: true}
	open := searchItem{Number: 9, CreatedAt: day("2026-10-01"), IsPR: true}
	src := &fakeSource{
		search: map[string]fakeSearch{
			"|" + r + " is:pr author:u":                             {total: 10},
			"|" + r + " is:issue author:u":                          {total: 4},
			"|" + r + " commenter:u -author:u":                      {total: 20},
			"|" + r + " is:pr reviewed-by:u -author:u":              {total: 5},
			"asc|" + r + " author:u":                                {total: 14, items: []searchItem{first}},
			"desc|" + r + " author:u":                               {total: 14, items: []searchItem{last}},
			"desc|" + r + " is:pr author:u is:open":                 {total: 1, items: []searchItem{open}},
			"|" + g:                                                 {total: 110},
			"|" + g + " is:merged":                                  {total: 40},
			"|" + g + " is:open":                                    {total: 30},
			"|" + g + " is:merged -repo:o/r":                        {total: 34},
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
	if want := r + " is:pr author:u is:merged sort:created-desc"; src.mergedQuery != want {
		t.Errorf("merged query = %q, want %q", src.mergedQuery, want)
	}
	if len(m.RecentMerged) != 2 || m.RecentMerged[0].Credit != 1 || m.RecentMerged[1].Credit != 0.5 {
		t.Errorf("RecentMerged = %+v", m.RecentMerged)
	}
	if got := m.CodeCredit(); got != 4.5 {
		t.Errorf("CodeCredit() = %v, want 4.5 (6 merged × 0.75)", got)
	}
	wantGlobal := Global{PRsOpened: 110, PRsMerged: 40, PRsOpen: 30, MergedElsewhere: 34,
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
		if !strings.Contains(q, "repo:o/r is:") && !strings.Contains(q, "repo:o/r author:") &&
			!strings.Contains(q, "repo:o/r commenter:") && strings.Contains(q, "is:pr author:u") {
			global = append(global, q)
		}
	}
	if len(global) != 7 {
		t.Fatalf("got %d global queries, want 7: %q", len(global), global)
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
	const q = "desc|is:pr is:merged is:public user:u -author:u"
	src := &fakeSource{
		stars: []int{34, 26, 3, 2, 1, 0},
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
		"desc|is:pr author:u is:public -user:u created:>=2026-09-29": {total: 2, items: []searchItem{{Repo: "z/z"}, {Repo: "a/a"}}},
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

func TestDriveBy(t *testing.T) {
	tests := []struct {
		name string
		m    Metrics
		want bool
	}{
		{"PRs only", Metrics{PRsOpened: 3}, true},
		{"PRs and a comment", Metrics{PRsOpened: 3, Threads: 1}, false},
		{"merged PRs, nothing else", Metrics{PRsOpened: 2, PRsMerged: 2}, false},
		{"nothing at all", Metrics{}, false},
		{"issues only", Metrics{Issues: 2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.DriveBy(); got != tt.want {
				t.Errorf("DriveBy() = %v, want %v", got, tt.want)
			}
		})
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
		{"one merged", Metrics{PRsOpened: 1, PRsMerged: 1}, 12},
		{"three merged", Metrics{PRsOpened: 3, PRsMerged: 3}, 24},
		{"three merged, all rewritten", Metrics{PRsOpened: 3, PRsMerged: 3, RecentMerged: reworked}, 0},
		{"one closed unmerged", Metrics{PRsOpened: 1}, -3},
		{"open PRs are neutral", Metrics{PRsOpened: 5, PRsOpen: 5}, 0},
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
	if !slices.Equal(groups, []string{groupHere, groupElsewhere, groupPattern}) {
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
	famous := Metrics{Global: Global{AccountAgeDays: 5000, PRsOpened: 6000, PRsMerged: 5000, MergedElsewhere: 5000}}
	r := score(famous, defaultWeights)
	if got := elsewherePoints(r); math.Abs(got-defaultWeights.ElsewhereCap) > 1e-9 {
		t.Errorf("elsewhere = %v, want cap %v", got, defaultWeights.ElsewhereCap)
	}
	if r.Tier != "Contributor" {
		t.Errorf("famous but new here: tier %q, want Contributor", r.Tier)
	}

	newbie := Metrics{Global: Global{AccountAgeDays: 30}}
	if slices.ContainsFunc(score(newbie, defaultWeights).Components, func(c Component) bool { return c.Label == "Cap" }) {
		t.Error("cap applied below the cap")
	}
}

func TestScoreNotLanding(t *testing.T) {
	notLanding := func(g Global) float64 {
		for _, c := range score(Metrics{Global: g}, defaultWeights).Components {
			if c.Group == groupPattern {
				return c.Points
			}
		}
		t.Fatal("no pattern component")
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
	if spray > -25 {
		t.Errorf("spray penalty = %.1f, want below -25", spray)
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
		RecentPRs: 108, RecentMerged: 3, RecentClosedUnmerged: 23, MergedElsewhere: 3}}
	// A real newcomer: established account, one closed PR here.
	newcomer := Metrics{PRsOpened: 1, Global: Global{AccountAgeDays: 1500, PRsOpened: 30, PRsMerged: 25, MergedElsewhere: 25, RecentPRs: 2}}

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
		Global: Global{AccountAgeDays: 3000, PRsOpened: 300, PRsMerged: 280, MergedElsewhere: 245}}
	if r := score(dormant, defaultWeights); r.Score < 80 {
		t.Errorf("dormant veteran scored %d (%s), want 80+", r.Score, r.Tier)
	}
}

func flagCodes(m Metrics) []string {
	var codes []string
	for _, f := range score(m, defaultWeights).Flags {
		codes = append(codes, f.Code)
	}
	return codes
}

func TestFlags(t *testing.T) {
	tests := []struct {
		name string
		m    Metrics
		want []string
	}{
		{"nothing", Metrics{}, nil},
		{"honest newcomer, one closed PR", Metrics{PRsOpened: 1, Threads: 2,
			Global: Global{AccountAgeDays: 1500, PRsOpened: 1, RecentPRs: 1, RecentClosedUnmerged: 1}}, nil},
		{"spray, mostly closed", Metrics{PRsOpened: 2, Global: Global{AccountCreated: testNow, AccountAgeDays: 17,
			PRsOpened: 108, PRsMerged: 3, PRsOpen: 82, MergedElsewhere: 3,
			RecentPRs: 108, RecentMerged: 3, RecentClosedUnmerged: 23, RecentRepos: 22}},
			[]string{flagNotLanding, flagNewBurst, flagDriveBy}},
		// The gap left by #2: nothing closed yet, so no penalty to speak of.
		{"spray, all still open", Metrics{Threads: 1, Global: Global{AccountCreated: testNow, AccountAgeDays: 3,
			PRsOpened: 100, PRsOpen: 100, RecentPRs: 100, RecentRepos: 40}},
			[]string{flagNewBurst}},
		{"new account, few repos", Metrics{Threads: 1, Global: Global{AccountCreated: testNow, AccountAgeDays: 30,
			PRsOpened: 30, PRsOpen: 30, RecentPRs: 30, RecentRepos: 3}}, nil},
		{"old account, many repos", Metrics{Threads: 1, Global: Global{AccountCreated: testNow, AccountAgeDays: 2000,
			PRsOpened: 60, PRsMerged: 50, RecentPRs: 40, RecentMerged: 35, RecentRepos: 30}}, nil},
		{"slow review queue", Metrics{PRsOpened: 10, PRsMerged: 3, PRsOpen: 7, Threads: 5,
			Global: Global{AccountCreated: testNow, AccountAgeDays: 1487, PRsOpened: 116, PRsMerged: 38, PRsOpen: 72,
				MergedElsewhere: 35, RecentPRs: 83, RecentMerged: 5, RecentClosedUnmerged: 6, RecentRepos: 8}}, nil},
		// Copilot on #18: a big penalty with nothing resolved can't claim
		// recent PRs "mostly didn't land." The new-account flag covers it.
		{"200 open, nothing resolved", Metrics{Threads: 1, Global: Global{AccountCreated: testNow, AccountAgeDays: 3,
			PRsOpened: 200, PRsOpen: 200, RecentPRs: 200, RecentRepos: 40}},
			[]string{flagNewBurst}},
		{"half of recent resolved merged", Metrics{Global: Global{AccountAgeDays: 3000,
			PRsOpened: 400, PRsMerged: 20, PRsOpen: 0, RecentPRs: 300, RecentMerged: 10, RecentClosedUnmerged: 10}}, nil},
		{"drive-by only", Metrics{PRsOpened: 1, PRsOpen: 1, Global: Global{AccountAgeDays: 400}}, []string{flagDriveBy}},
		{"merged here, nothing else", Metrics{PRsOpened: 2, PRsMerged: 2, Global: Global{AccountAgeDays: 5000}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flagCodes(tt.m); !slices.Equal(got, tt.want) {
				t.Errorf("flags = %v, want %v", got, tt.want)
			}
		})
	}
}

func repoItems(n int, repo func(i int) string) []searchItem {
	items := make([]searchItem, n)
	for i := range items {
		items[i] = searchItem{Repo: repo(i)}
	}
	return items
}

func TestCollectReadsMorePagesToCountRepos(t *testing.T) {
	// Copilot on #18: the newest 100 PRs all went to one repo and the older 9
	// to nine others. The first page alone would show 1 repo.
	const q = "is:pr author:u is:public -user:u created:>=2026-09-29"
	src := &fakeSource{search: map[string]fakeSearch{
		"desc|" + q:            {total: 109, items: repoItems(100, func(int) string { return "a/a" })},
		"desc|" + q + "|page2": {total: 109, items: repoItems(9, func(i int) string { return fmt.Sprintf("r/%d", i) })},
	}}
	m, err := collect(src, "o/r", account{Login: "u", CreatedAt: testNow.AddDate(0, 0, -5)}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if m.Global.RecentRepos != 10 || m.Global.RecentSampled != 109 {
		t.Errorf("repos = %d from %d PRs, want 10 from 109", m.Global.RecentRepos, m.Global.RecentSampled)
	}
	if !slices.Contains(flagCodes(m), flagNewBurst) {
		t.Errorf("new-account flag missing: %v", flagCodes(m))
	}
}

func TestCollectStopsPagingOnceReposAreKnown(t *testing.T) {
	const q = "is:pr author:u is:public -user:u created:>=2026-09-29"
	src := &fakeSource{search: map[string]fakeSearch{
		"desc|" + q: {total: 150, items: repoItems(100, func(i int) string { return fmt.Sprintf("r/%d", i%12) })},
	}}
	m, err := collect(src, "o/r", account{Login: "u", CreatedAt: testNow.AddDate(0, 0, -5)}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(src.queries, func(q string) bool { return strings.Contains(q, "|page") }) {
		t.Errorf("read more pages after finding 12 repos: %q", src.queries)
	}
	// Counted from 100 of 150 PRs, so the message says it's a lower bound.
	r := score(m, defaultWeights)
	i := slices.IndexFunc(r.Flags, func(f Flag) bool { return f.Code == flagNewBurst })
	if i < 0 || !strings.Contains(r.Flags[i].Message, "150 PRs across at least 12 repos") {
		t.Errorf("flags = %+v", r.Flags)
	}
}

func TestFlagsDontChangeScore(t *testing.T) {
	m := Metrics{Global: Global{AccountCreated: testNow, AccountAgeDays: 3, PRsOpened: 100, PRsOpen: 100, RecentPRs: 100, RecentRepos: 40}}
	r := score(m, defaultWeights)
	var sum float64
	for _, c := range r.Components {
		sum += c.Points
	}
	if len(r.Flags) == 0 || int(math.Round(sum)) != r.Score {
		t.Errorf("flags %v, components sum %.1f, score %d", r.Flags, sum, r.Score)
	}
}

func TestRenderFlagsInHeader(t *testing.T) {
	m := Metrics{User: "u", Repo: "o/r", Global: Global{AccountCreated: testNow, AccountAgeDays: 6,
		PRsOpened: 50, PRsMerged: 26, RecentPRs: 50, RecentMerged: 26, RecentRepos: 35, RecentSampled: 50}, Threads: 1}
	var buf bytes.Buffer
	if err := renderText(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(buf.String(), "\n")
	want := "  ⚠ New account: 6 days old, 50 PRs across 35 repos in 30 days, 26 merged so far"
	if len(lines) < 2 || lines[1] != want {
		t.Errorf("line 2 = %q, want %q", lines[1], want)
	}
}

func TestRenderJSONFlags(t *testing.T) {
	var buf bytes.Buffer
	m := Metrics{User: "u", Repo: "o/r"}
	if err := renderJSON(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	// An empty list, not null, so scripts can always iterate it.
	if !strings.Contains(buf.String(), `"flags": []`) {
		t.Errorf("missing empty flags array:\n%s", buf.String())
	}
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
	var got struct {
		SchemaVersion int                `json:"schema_version"`
		Version       string             `json:"version"`
		GeneratedAt   time.Time          `json:"generated_at"`
		Weights       map[string]float64 `json:"weights"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != schemaVersion || got.Version != "v9.9.9" || !got.GeneratedAt.Equal(testNow) {
		t.Errorf("meta = %+v", got)
	}
	// Every weight is recorded, so an old score can be recomputed.
	if len(got.Weights) != reflect.TypeFor[Weights]().NumField() {
		t.Errorf("weights has %d fields, Weights has %d: %v", len(got.Weights), reflect.TypeFor[Weights]().NumField(), got.Weights)
	}
	if got.Weights["code"] != defaultWeights.Code || got.Weights["not_landing"] != defaultWeights.NotLanding {
		t.Errorf("weights = %v", got.Weights)
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

func TestRetryNotice(t *testing.T) {
	got := retryNotice(time.Minute, 1, time.Date(2026, 10, 29, 9, 40, 0, 0, time.UTC))
	want := "GitHub rate limit hit. Waiting 1m0s (until 09:41:00) before retrying, attempt 1 of 3. Not hung."
	if got != want {
		t.Errorf("retryNotice() =\n%q\nwant\n%q", got, want)
	}
}

func TestRepoFromURL(t *testing.T) {
	if got := repoFromURL("https://api.github.com/repos/o/r"); got != "o/r" {
		t.Errorf("repoFromURL() = %q", got)
	}
}

func TestScoreHelpfulBeatsDriveBy(t *testing.T) {
	regular := Metrics{PRsOpened: 5, PRsMerged: 4, Issues: 3, Threads: 15, ThumbsUp: 10}
	driveBy := Metrics{PRsOpened: 30, PRsMerged: 1}
	if r, d := score(regular, defaultWeights).Score, score(driveBy, defaultWeights).Score; r <= d {
		t.Errorf("regular contributor scored %d, drive-by scored %d", r, d)
	}
}

func TestTierFor(t *testing.T) {
	for score, want := range map[int]string{-10: "Novice", 0: "Novice", 14: "Novice", 15: "Contributor", 40: "Regular", 149: "Veteran", 150: "Core", 900: "Core"} {
		if got := tierFor(score); got != want {
			t.Errorf("tierFor(%d) = %q, want %q", score, got, want)
		}
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
			PRsOpened: 1300, PRsMerged: 1250, PRsOpen: 12, MergedElsewhere: 50,
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
		"Code credit (1,200 merged × 0.88)",
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
		`Cap \(elsewhere is at most \+20\) +-9\.7`,
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

func TestRenderTextDriveBy(t *testing.T) {
	m := Metrics{User: "u", Repo: "o/r", PRsOpened: 2, PRsOpen: 2,
		FirstAuthored: &searchItem{Number: 1, CreatedAt: day("2026-10-01"), IsPR: true}}
	var buf bytes.Buffer
	if err := renderText(&buf, m, score(m, defaultWeights), testMeta); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Novice", "⚠ Drive-by:", "PR #1, 2026-10-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
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
	if got["user"] != "u" || got["averyindex"] != float64(24) || got["tier"] != "Contributor" {
		t.Errorf("unexpected JSON: %s", buf.String())
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
