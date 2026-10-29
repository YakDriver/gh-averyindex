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
	// 30 days) and by (1 - landing rate)², so it only bites when volume is
	// high and PRs aren't landing. See Global.LandingRate.
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
	Flags      []Flag      `json:"flags"`
}

// Flag is something a maintainer should look at more closely. Flags state
// facts; they don't change the score.
type Flag struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	flagNotLanding = "not-landing"
	flagNewBurst   = "new-account-burst"
	flagDriveBy    = "drive-by"
)

const (
	// notLandingFlagPoints is how negative the Pattern penalty must be, and
	// it must also outweigh everything else, before it's flagged. The floor
	// keeps a newcomer with one closed PR from being flagged.
	notLandingFlagPoints = -10
	// notLandingMinResolved is how many recent PRs must have resolved before
	// the flag can say they mostly didn't land. The penalty can be large with
	// nothing resolved (the 50% prior times high volume), but then there's no
	// recent outcome to report; the new-account flag covers that case.
	notLandingMinResolved = 5

	// A new account opening many PRs across many repos is flagged even
	// before any are closed.
	burstMaxAgeDays = 90
	burstMinPRs     = 20
	burstMinRepos   = 10
)

func flags(m Metrics, r Result) []Flag {
	g := m.Global
	var pattern, rest float64
	for _, c := range r.Components {
		if c.Group == groupPattern {
			pattern += c.Points
		} else {
			rest += c.Points
		}
	}

	fs := []Flag{}
	resolved := g.RecentMerged + g.RecentClosedUnmerged
	if pattern <= notLandingFlagPoints && -pattern > rest &&
		resolved >= notLandingMinResolved && 2*g.RecentMerged < resolved {
		fs = append(fs, Flag{flagNotLanding, fmt.Sprintf(
			"Recent PRs mostly not landing: %s of %s resolved merged in %d days",
			thousands(g.RecentMerged), thousands(resolved), recentDays)})
	}
	if !g.AccountCreated.IsZero() && g.AccountAgeDays < burstMaxAgeDays &&
		g.RecentPRs >= burstMinPRs && g.RecentRepos >= burstMinRepos {
		repos := fmt.Sprintf("%d repos", g.RecentRepos)
		if g.RecentSampled < g.RecentPRs {
			// Repos are counted from a sample, so this is a lower bound.
			repos = "at least " + repos
		}
		fs = append(fs, Flag{flagNewBurst, fmt.Sprintf(
			"New account: %s old, %s PRs across %s in %d days, %s merged so far",
			age(g.AccountAgeDays), thousands(g.RecentPRs), repos, recentDays, thousands(g.RecentMerged))})
	}
	if m.DriveBy() {
		fs = append(fs, Flag{flagDriveBy, "Drive-by: PRs here, none merged, and no issues, comments, or reviews"})
	}
	return fs
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

	miss := 1 - g.LandingRate()
	notLanding := Component{
		Label: "Recent PRs not landing",
		Detail: fmt.Sprintf("%s in %d days · %s of %s merged · history %s of %s",
			thousands(g.RecentPRs), recentDays,
			thousands(g.RecentMerged), thousands(g.RecentMerged+g.RecentClosedUnmerged),
			thousands(g.HistoryMerged()), thousands(g.HistoryResolved())),
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
	r.Flags = flags(m, r)
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
