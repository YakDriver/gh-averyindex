package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
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
	err         error
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

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCollect(t *testing.T) {
	const r = "repo:o/r"
	first := searchItem{Number: 1, CreatedAt: day("2020-01-01")}
	last := searchItem{Number: 9, CreatedAt: day("2026-10-01"), IsPR: true}
	open := searchItem{Number: 9, CreatedAt: day("2026-10-01"), IsPR: true}
	src := &fakeSource{
		search: map[string]fakeSearch{
			"|" + r + " is:pr author:u":                {total: 10},
			"|" + r + " is:issue author:u":             {total: 4},
			"|" + r + " commenter:u -author:u":         {total: 20},
			"|" + r + " is:pr reviewed-by:u -author:u": {total: 5},
			"asc|" + r + " author:u":                   {total: 14, items: []searchItem{first}},
			"desc|" + r + " author:u":                  {total: 14, items: []searchItem{last}},
			"desc|" + r + " is:pr author:u is:open":    {total: 1, items: []searchItem{open}},
		},
		merged: 6,
		prs: []rawPR{
			{Number: 5, Commits: []rawCommit{{Logins: []string{"u"}, Additions: 100}}},
			{Number: 4, Commits: []rawCommit{{Logins: []string{"u"}, Additions: 100}, {Logins: []string{"m"}, Additions: 50}}},
		},
		thumbs: 9,
	}

	m, err := collect(src, "o/r", "u")
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
	for key := range src.search {
		if !slices.Contains(src.queries, key) {
			t.Errorf("query not issued: %q", key)
		}
	}
}

func TestCollectNoPRsSkipsPRQueries(t *testing.T) {
	src := &fakeSource{}
	m, err := collect(src, "o/r", "u")
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
	if _, err := collect(src, "o/r", "u"); err == nil {
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
	}
	var buf bytes.Buffer
	if err := renderText(&buf, m, score(m, defaultWeights)); err != nil {
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
		"averyindex",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
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
	if err := renderText(&buf, m, score(m, defaultWeights)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Novice", "Drive-by:", "PR #1, 2026-10-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTextNoActivity(t *testing.T) {
	var buf bytes.Buffer
	m := Metrics{User: "u", Repo: "o/r"}
	if err := renderText(&buf, m, score(m, defaultWeights)); err != nil {
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
	if err := renderJSON(&buf, m, score(m, defaultWeights)); err != nil {
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
