package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"
)

func renderText(w io.Writer, m Metrics, r Result) error {
	fmt.Fprintf(w, "%s in %s  ·  %s  ·  averyindex %d\n", m.User, m.Repo, r.Tier, r.Score)

	fmt.Fprintln(w, "\nParticipation")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	upvotes := thousands(m.ThumbsUp)
	if m.ThumbsUpTruncated {
		upvotes += " (first 1,000 items)"
	}
	for _, row := range [][2]string{
		{"Issues opened", thousands(m.Issues)},
		{"Threads commented", thousands(m.Threads)},
		{"PRs reviewed", thousands(m.Reviews)},
		{"Upvotes received", upvotes},
		{"First authored", describe(m.FirstAuthored)},
		{"Last authored", describe(m.LastAuthored)},
	} {
		fmt.Fprintf(tw, "  %s\t%s\n", row[0], row[1])
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if m.DriveBy() {
		fmt.Fprintln(w, "  Drive-by: pull requests only, no issues, comments, or reviews")
	}

	if err := renderPRs(w, m); err != nil {
		return err
	}

	renderGlobal(w, m.Global)

	fmt.Fprintln(w, "\nScore")
	labels := make([]string, len(r.Components))
	width := len("averyindex")
	for i, c := range r.Components {
		labels[i] = c.Label
		if c.Detail != "" {
			labels[i] += " (" + c.Detail + ")"
		}
		// Component rows are indented two more than group headers.
		width = max(width, utf8.RuneCountInString(labels[i])+2)
	}
	group := ""
	for i, c := range r.Components {
		if c.Group != group {
			group = c.Group
			fmt.Fprintf(w, "  %s\n", group)
		}
		n := count(c.Count)
		if c.Label == "Cap" {
			n = ""
		}
		fmt.Fprintf(w, "    %s%9s  %7s\n", pad(labels[i], width-2), n, signed(c.Points))
	}
	fmt.Fprintf(w, "  %s%9s  %7d\n", pad("averyindex", width), r.Tier, r.Score)
	return nil
}

// pad right-pads s with spaces to width runes.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-utf8.RuneCountInString(s)))
}

func renderGlobal(w io.Writer, g Global) {
	fmt.Fprintln(w, "\nElsewhere on GitHub (public repos the user doesn't own)")
	created := "unknown"
	if !g.AccountCreated.IsZero() {
		created = fmt.Sprintf("%s (%s ago)", date(g.AccountCreated), age(g.AccountAgeDays))
	}
	rate := "no resolved PRs"
	if r, ok := g.MergeRate(); ok {
		rate = fmt.Sprintf("%.0f%% of %s resolved", r*100, thousands(g.PRsMerged+g.ClosedUnmerged()))
	}
	recent := fmt.Sprintf("%s PRs", thousands(g.RecentPRs))
	if g.RecentPRs > 0 {
		recent += fmt.Sprintf(" across %d repos (most: %d in %s)", g.RecentRepos, g.RecentTopCount, g.RecentTopRepo)
		if g.RecentPRs > g.RecentSampled {
			recent += fmt.Sprintf(", repos from the newest %d", g.RecentSampled)
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range [][2]string{
		{"Account created", created},
		{"PRs opened", fmt.Sprintf("%s · %s merged · %s open · %s closed unmerged",
			thousands(g.PRsOpened), thousands(g.PRsMerged), thousands(g.PRsOpen), thousands(g.ClosedUnmerged()))},
		{"Merge rate", rate},
		{fmt.Sprintf("Last %d days", recentDays), recent},
	} {
		fmt.Fprintf(tw, "  %s\t%s\n", row[0], row[1])
	}
	tw.Flush()
}

// count formats whole numbers with separators and fractions to one place.
func count(f float64) string {
	if f == math.Trunc(f) {
		return thousands(int(f))
	}
	return fmt.Sprintf("%.1f", f)
}

func renderPRs(w io.Writer, m Metrics) error {
	if m.PRsOpened == 0 {
		fmt.Fprintln(w, "\nPull requests  none")
		return nil
	}
	fmt.Fprintf(w, "\nPull requests  %s merged · %s open · %s closed unmerged\n",
		thousands(m.PRsMerged), thousands(m.PRsOpen), thousands(m.PRsClosedUnmerged()))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, pr := range m.RecentMerged {
		note := ""
		if len(pr.CommitsBy) > 0 {
			note += "  (commits by " + strings.Join(pr.CommitsBy, ", ") + ")"
		}
		if pr.CommitsTruncated {
			note += fmt.Sprintf("  (first %d commits)", maxCommits)
		}
		fmt.Fprintf(tw, "  #%d\tmerged %s\t+%s -%s\trework %3.0f%%\tcredit %.2f%s\n",
			pr.Number, date(pr.MergedAt), thousands(pr.Additions), thousands(pr.Deletions),
			pr.Rework*100, pr.Credit, note)
	}
	for _, pr := range m.OpenPRs {
		fmt.Fprintf(tw, "  #%d\topen since %s\n", pr.Number, date(pr.CreatedAt))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if more := m.PRsOpen - len(m.OpenPRs); more > 0 {
		fmt.Fprintf(w, "  and %s more open\n", thousands(more))
	}
	if m.PRsMerged > len(m.RecentMerged) && len(m.RecentMerged) > 0 {
		fmt.Fprintf(w, "  Rework checked on the %d most recent merged PRs.\n", len(m.RecentMerged))
	}
	return nil
}

func renderJSON(w io.Writer, m Metrics, r Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Metrics
		Result
	}{m, r})
}

func describe(item *searchItem) string {
	if item == nil {
		return "never"
	}
	return fmt.Sprintf("%s #%d, %s", item.kind(), item.Number, date(item.CreatedAt))
}

func signed(f float64) string {
	// Avoid printing "-0.0".
	if math.Abs(f) < 0.05 {
		f = 0
	}
	return fmt.Sprintf("%+.1f", f)
}

func date(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format(time.DateOnly)
}

// thousands formats n with comma separators.
func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := n < 0
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
