package main

import (
	"fmt"
	"math"
)

// The score has three groups:
//
//   - Here: work in this repository. This is most of the score.
//   - Elsewhere: account age and PRs merged in other repositories. This gives
//     an established developer some benefit of the doubt in a repo they're new
//     to. It's capped so it can't substitute for work here.
//   - Pattern: a penalty for opening many PRs that don't land. It's uncapped.
const (
	groupHere      = "Here"
	groupElsewhere = "Elsewhere"
	groupPattern   = "Pattern"
)

// Weights are points per doubling of each count. Every count goes through
// log2(1+n), so the first few contributions matter most and huge counts don't
// drown out everything else: with a weight of 12, 1 merged PR with full credit
// is 12 points, 3 is 24, 7 is 36.
//
// Signals that depend on someone else's judgment (a maintainer merged it and
// didn't rewrite it, other users upvoted it) weigh more than signals the user
// produces alone (comments, issues), because they're harder to inflate.
//
// These are a first guess. Tune them by scoring accounts you already have
// opinions about until the ranking matches your judgment.
type Weights struct {
	// Here.
	Code           float64
	ThumbsUp       float64
	Reviews        float64
	Threads        float64
	Issues         float64
	ClosedUnmerged float64

	// Elsewhere. AccountAge is per doubling of account age in months.
	AccountAge      float64
	MergedElsewhere float64
	ProjectStars    float64
	Contributors    float64
	ElsewhereCap    float64

	// Pattern. NotLanding is multiplied by log2(1 + PRs opened in the last
	// 30 days) and by (1 - merge rate)², so it only bites when volume is
	// high and the merge rate is low.
	NotLanding float64
}

var defaultWeights = Weights{
	Code:           12,
	ThumbsUp:       5,
	Reviews:        5,
	Threads:        2,
	Issues:         2,
	ClosedUnmerged: -3,

	AccountAge:      1.5,
	MergedElsewhere: 4,
	ProjectStars:    2,
	Contributors:    4,
	ElsewhereCap:    20,

	NotLanding: -6,
}

// tiers are minimum scores for each tier, highest first. Anything below the
// last one is "Novice".
var tiers = []struct {
	min  int
	name string
}{
	{150, "Core"},
	{80, "Veteran"},
	{40, "Regular"},
	{15, "Contributor"},
}

func tierFor(score int) string {
	for _, t := range tiers {
		if score >= t.min {
			return t.name
		}
	}
	return "Novice"
}

// Component is one line of the score breakdown.
type Component struct {
	Group  string  `json:"group"`
	Label  string  `json:"label"`
	Detail string  `json:"detail,omitempty"`
	Count  float64 `json:"count"`
	Points float64 `json:"points"`
}

// Result is the score and the components that produced it.
type Result struct {
	Components []Component `json:"components"`
	Score      int         `json:"averyindex"`
	Tier       string      `json:"tier"`
}

func points(n, weight float64) float64 {
	if n <= 0 {
		return 0
	}
	return weight * math.Log2(1+n)
}

const daysPerMonth = 365.25 / 12

func score(m Metrics, w Weights) Result {
	g := m.Global
	months := float64(g.AccountAgeDays) / daysPerMonth

	here := []Component{
		{Label: "Code credit", Detail: fmt.Sprintf("%s merged × %.2f", thousands(m.PRsMerged), m.AvgCredit()),
			Count: m.CodeCredit(), Points: points(m.CodeCredit(), w.Code)},
		{Label: "Upvotes received", Count: float64(m.ThumbsUp), Points: points(float64(m.ThumbsUp), w.ThumbsUp)},
		{Label: "PRs reviewed", Count: float64(m.Reviews), Points: points(float64(m.Reviews), w.Reviews)},
		{Label: "Threads commented", Count: float64(m.Threads), Points: points(float64(m.Threads), w.Threads)},
		{Label: "Issues opened", Count: float64(m.Issues), Points: points(float64(m.Issues), w.Issues)},
		{Label: "PRs closed unmerged", Count: float64(m.PRsClosedUnmerged()),
			Points: points(float64(m.PRsClosedUnmerged()), w.ClosedUnmerged)},
	}

	elsewhere := []Component{
		{Label: "Account age", Detail: age(g.AccountAgeDays), Count: months, Points: points(months, w.AccountAge)},
		{Label: "PRs merged elsewhere", Count: float64(g.MergedElsewhere),
			Points: points(float64(g.MergedElsewhere), w.MergedElsewhere)},
		{Label: "Stars on own projects", Detail: fmt.Sprintf("%d repos with %d+", g.ProjectsStarred, minProjectStars),
			Count: float64(g.ProjectStars), Points: points(float64(g.ProjectStars), w.ProjectStars)},
		{Label: "Contributors to own projects", Count: float64(g.Contributors),
			Points: points(float64(g.Contributors), w.Contributors)},
	}
	var elsewhereTotal float64
	for _, c := range elsewhere {
		elsewhereTotal += c.Points
	}
	if elsewhereTotal > w.ElsewhereCap {
		elsewhere = append(elsewhere, Component{
			Label:  "Cap",
			Detail: fmt.Sprintf("elsewhere is at most %+.0f", w.ElsewhereCap),
			Points: w.ElsewhereCap - elsewhereTotal,
		})
	}

	miss := 1 - g.SmoothedMergeRate()
	notLanding := Component{
		Label:  "Recent PRs not landing",
		Detail: fmt.Sprintf("%s in %d days, %.0f%% merge rate", thousands(g.RecentPRs), recentDays, g.SmoothedMergeRate()*100),
		Count:  float64(g.RecentPRs),
		Points: points(float64(g.RecentPRs), w.NotLanding) * miss * miss,
	}

	var r Result
	var total float64
	for _, grp := range []struct {
		name  string
		parts []Component
	}{
		{groupHere, here},
		{groupElsewhere, elsewhere},
		{groupPattern, []Component{notLanding}},
	} {
		for _, c := range grp.parts {
			c.Group = grp.name
			total += c.Points
			r.Components = append(r.Components, c)
		}
	}
	r.Score = int(math.Round(total))
	r.Tier = tierFor(r.Score)
	return r
}

// age describes an account age in days, months, or years.
func age(days int) string {
	switch {
	case days < 60:
		return fmt.Sprintf("%d days", days)
	case days < 730:
		return fmt.Sprintf("%.0f months", float64(days)/daysPerMonth)
	default:
		return fmt.Sprintf("%.1f years", float64(days)/365.25)
	}
}
