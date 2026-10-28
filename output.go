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

	fmt.Fprintln(w, "\nScore")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range r.Components {
		label, count := c.Label, thousands(int(c.Count))
		if c.Label == codeCreditLabel {
			label = fmt.Sprintf("%s (%s merged × %.2f)", c.Label, thousands(m.PRsMerged), m.AvgCredit())
			count = fmt.Sprintf("%.1f", c.Count)
		}
		fmt.Fprintf(tw, "  %s\t%9s\t%7s\n", label, count, signed(c.Points))
	}
	fmt.Fprintf(tw, "  averyindex\t%9s\t%7d\n", r.Tier, r.Score)
	return tw.Flush()
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
