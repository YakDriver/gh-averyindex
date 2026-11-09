package main

import (
	"fmt"
	"math"
)

// averyindex is the sum of three scores (docs/design/scoring-v2.md):
//
//   - Here: what the user has given this repository. This is most of the
//     score.
//   - Elsewhere: the user's record outside this repository. Its positive
//     part is capped, so it gives an established developer some benefit of
//     the doubt without standing in for work here. Its negative part, for
//     PRs that don't land and authoring without engagement, isn't.
//   - Burden: what the user costs maintainers here: heavy maintainer
//     involvement, and authoring without engagement. It's never positive.
const (
	groupHere      = "Here"
	groupElsewhere = "Elsewhere"
	groupBurden    = "Burden"
)

// Weights are points per doubling of each count. Every count goes through
// log2(1+n), so the first few contributions matter most and huge counts don't
// drown out everything else: with a weight of 12, 1 merged PR with full credit
// is 12 points, 3 is 24, 7 is 36.
//
// Signals that depend on someone else's judgment (a maintainer merged it and
// didn't rewrite it, other users upvoted it) weigh more than signals the user
// produces alone (comments, issues), because they're harder to inflate.
type Weights struct {
	// Here.
	Code     float64
	ThumbsUp float64
	Reviews  float64
	Threads  float64
	Issues   float64

	// Elsewhere. AccountAge is per doubling of account age in months.
	AccountAge      float64
	MergedElsewhere float64
	ProjectStars    float64
	Contributors    float64
	ElsewhereCap    float64
	// NotLanding is multiplied by log2(1 + PRs opened in the last 30 days)
	// and by (1 - landing rate)², so it only bites when volume is high and
	// PRs aren't landing. See Global.LandingRate.
	NotLanding float64
	// Spray is a logistic curve in the number of repos where PRs were
	// closed without landing: about SprayMax/2 at SprayMid repos, with
	// SpraySteep setting how quickly it rises.
	SprayMax   float64
	SprayMid   float64
	SpraySteep float64

	// Burden, and the matching line in Elsewhere. See authoringPoints.
	Authoring         float64
	AuthoringTarget   float64
	AuthoringFullRate float64
}

var defaultWeights = Weights{
	Code:     12,
	ThumbsUp: 5,
	Reviews:  5,
	Threads:  2,
	Issues:   2,

	AccountAge:      1.5,
	MergedElsewhere: 4,
	ProjectStars:    2,
	Contributors:    4,
	ElsewhereCap:    20,
	NotLanding:      -6,
	SprayMax:        -15,
	SprayMid:        20,
	SpraySteep:      4,

	Authoring:         -5,
	AuthoringTarget:   1,
	AuthoringFullRate: 10,
}

// authoringGrace is how many PRs anyone can open before the balance between
// authoring and everything else counts.
const authoringGrace = 3

// authoringPoints is the cost of authoring without engagement, the pattern
// of an AI PR factory: many PRs, fast, with few merged and little else.
//
// It's a ratio. For each PR opened, the user should have about
// AuthoringTarget merged PRs plus issues, comments, and reviews of other
// people's work. The cost grows with how far short of that they fall
// (squared, so falling a little short costs little) and with how many PRs
// they opened past the grace. Someone who reviews and discusses as much as
// they author pays nothing, however many PRs were closed.
//
// It's also a rate. A factory opens dozens of PRs a month. Someone who opens
// a PR every month or two and does nothing else is authoring only, but
// that's an ordinary contributor. The cost is full at AuthoringFullRate PRs
// a month, and scales down below that.
func authoringPoints(prs, merged, gave int, months float64, w Weights) float64 {
	if prs <= authoringGrace {
		return 0
	}
	short := max(0, 1-float64(merged+gave)/(w.AuthoringTarget*float64(prs)))
	rate := float64(prs) / max(1, months)
	return points(float64(prs-authoringGrace), w.Authoring) * short * short * min(1, rate/w.AuthoringFullRate)
}

// sprayPoints is the spray penalty for n repos, shifted so 0 repos is 0.
func sprayPoints(n int, w Weights) float64 {
	curve := func(x float64) float64 { return w.SprayMax / (1 + math.Exp(-(x-w.SprayMid)/w.SpraySteep)) }
	return curve(float64(n)) - curve(0)
}

// Component is one line of the score breakdown.
type Component struct {
	Group  string  `json:"group"`
	Label  string  `json:"label"`
	Detail string  `json:"detail,omitempty"`
	Count  float64 `json:"count"`
	Points float64 `json:"points"`

	// why describes the component in a few words for the summary line, or
	// is empty if it's never the reason.
	why string
}

// Result is the score, its three parts, and the components that produced
// them. Score is the sum of the rounded parts, so the summary adds up.
type Result struct {
	Components []Component `json:"components"`
	Here       int         `json:"here"`
	Elsewhere  int         `json:"elsewhere"`
	Burden     int         `json:"burden"`
	Score      int         `json:"averyindex"`
	// Reason is the biggest factor in the score, in a few words.
	Reason string `json:"reason"`
}

func points(n, weight float64) float64 {
	if n <= 0 {
		return 0
	}
	return weight * math.Log2(1+n)
}

const daysPerMonth = 365.25 / 12

// A cost is the reason only when it takes at least reasonMinCost points and
// at least reasonShare of what the user earned. Otherwise the score is
// explained by what they've given: 101 PRs closed unmerged isn't the story
// for someone with 1,686 merged.
const (
	reasonMinCost = -3
	reasonShare   = 0.25
)

func score(m Metrics, w Weights) Result {
	g := m.Global
	months := float64(g.AccountAgeDays) / daysPerMonth

	// Heavy PRs earn nothing. Here counts them at the usual credit and
	// Burden takes the difference back, so the cost is visible.
	gross := float64(m.PRsMerged) * m.AvgCredit()
	net := m.CodeCredit()
	here := []Component{
		{Label: "Code credit", Detail: fmt.Sprintf("%s merged × %.2f", thousands(m.PRsMerged), m.AvgCredit()),
			Count: gross, Points: points(gross, w.Code), why: plural(m.PRsMerged, "PR") + " merged here"},
		{Label: "Upvotes received", Count: float64(m.ThumbsUp), Points: points(float64(m.ThumbsUp), w.ThumbsUp),
			why: plural(m.ThumbsUp, "upvote") + " here"},
		{Label: "PRs reviewed", Count: float64(m.Reviews), Points: points(float64(m.Reviews), w.Reviews),
			why: "reviewed " + plural(m.Reviews, "PR") + " here"},
		{Label: "Threads commented", Count: float64(m.Threads), Points: points(float64(m.Threads), w.Threads),
			why: "commented on " + plural(m.Threads, "thread") + " here"},
		{Label: "Issues opened", Count: float64(m.Issues), Points: points(float64(m.Issues), w.Issues),
			why: "opened " + plural(m.Issues, "issue") + " here"},
	}

	elsewhere := []Component{
		{Label: "Account age", Detail: age(g.AccountAgeDays), Count: months, Points: points(months, w.AccountAge),
			why: "account " + age(g.AccountAgeDays) + " old"},
		{Label: "PRs merged elsewhere", Detail: acceptedDetail(g), Count: g.AcceptedMergedElsewhere(),
			Points: points(g.AcceptedMergedElsewhere(), w.MergedElsewhere), why: plural(g.MergedElsewhere, "PR") + " merged elsewhere"},
		{Label: "Stars on own projects", Detail: fmt.Sprintf("%d repos with %d+", g.ProjectsStarred, minProjectStars),
			Count: float64(g.ProjectStars), Points: points(float64(g.ProjectStars), w.ProjectStars),
			why: plural(g.ProjectStars, "star") + " on own projects"},
		{Label: "Contributors to own projects", Count: float64(g.Contributors),
			Points: points(float64(g.Contributors), w.Contributors), why: plural(g.Contributors, "contributor") + " to own projects"},
	}
	var positive float64
	for _, c := range elsewhere {
		positive += c.Points
	}
	if positive > w.ElsewhereCap {
		elsewhere = append(elsewhere, Component{
			Label:  "Cap",
			Detail: fmt.Sprintf("benefit of the doubt is at most %+.0f", w.ElsewhereCap),
			Points: w.ElsewhereCap - positive,
		})
	}
	miss := 1 - g.LandingRate()
	resolved := g.RecentMerged + g.RecentClosedUnmerged
	gave := g.RecentGave()
	elsewhere = append(elsewhere,
		Component{
			Label: "Recent PRs not landing",
			Detail: fmt.Sprintf("%s in %d days · %s of %s merged · history %s of %s",
				thousands(g.RecentPRs), recentDays, thousands(g.RecentMerged), thousands(resolved),
				thousands(g.HistoryMerged()), thousands(g.HistoryResolved())),
			Count:  float64(g.RecentPRs),
			Points: points(float64(g.RecentPRs), w.NotLanding) * miss * miss,
			why:    fmt.Sprintf("%s of %s PRs elsewhere resolved in %d days merged", thousands(g.RecentMerged), thousands(resolved), recentDays),
		},
		Component{
			Label:  "PRs closed unmerged in repos",
			Detail: fmt.Sprintf("days %d to %d · from %s", recentDays+1, sprayDays, plural(g.SpraySampled, "PR")),
			Count:  float64(g.SprayRepos),
			Points: sprayPoints(g.SprayRepos, w),
			why:    fmt.Sprintf("PRs closed unmerged in %s elsewhere", plural(g.SprayRepos, "repo")),
		},
		Component{
			Label: "Authoring without engagement elsewhere",
			Detail: fmt.Sprintf("%d days · %s · %s merged · %s given",
				recentDays, plural(g.RecentPRs, "PR"), thousands(g.RecentMerged), thousands(gave)),
			Count:  float64(g.RecentPRs),
			Points: authoringPoints(g.RecentPRs, g.RecentMerged, gave, recentDays/daysPerMonth, w),
			why: fmt.Sprintf("%s elsewhere in %d days, %s merged, %s", plural(g.RecentPRs, "PR"),
				recentDays, thousands(g.RecentMerged), engagement(gave)),
		},
	)

	burden := []Component{
		{Label: "Heavy maintainer involvement", Detail: heavyDetail(m),
			Count: float64(len(m.HeavyPRs)), Points: points(net, w.Code) - points(gross, w.Code),
			why: plural(len(m.HeavyPRs), "merged PR") + " here needed heavy maintainer work"},
		{Label: "Authoring without engagement", Detail: fmt.Sprintf("%s in %s · %s merged · %s given",
			plural(m.PRsOpened, "PR"), monthsSpan(m.ActiveDays), thousands(m.PRsMerged), thousands(m.Participation())),
			Count:  float64(m.PRsOpened),
			Points: authoringPoints(m.PRsOpened, m.PRsMerged, m.Participation(), float64(m.ActiveDays)/daysPerMonth, w),
			why:    fmt.Sprintf("%s here, %s merged, %s", plural(m.PRsOpened, "PR"), thousands(m.PRsMerged), engagement(m.Participation()))},
	}

	var r Result
	for _, grp := range []struct {
		name  string
		parts []Component
		total *int
	}{
		{groupHere, here, &r.Here},
		{groupElsewhere, elsewhere, &r.Elsewhere},
		{groupBurden, burden, &r.Burden},
	} {
		var sum float64
		for _, c := range grp.parts {
			c.Group = grp.name
			sum += c.Points
			r.Components = append(r.Components, c)
		}
		*grp.total = int(math.Round(sum))
	}
	r.Score = r.Here + r.Elsewhere + r.Burden
	// Elsewhere's gains are ranked at what they're worth after the cap.
	scale := 1.0
	if positive > w.ElsewhereCap {
		scale = w.ElsewhereCap / positive
	}
	r.Reason = reason(r.Components, float64(r.Here)+min(positive, w.ElsewhereCap), scale)
	return r
}

func heavyDetail(m Metrics) string {
	if len(m.HeavyPRs) == 0 {
		return ""
	}
	return plural(len(m.HeavyPRs), "merged PR") + " earn no credit"
}

// reason picks the biggest cost if it's large enough next to what the user
// earned, and otherwise the biggest source of points. New components feed it
// without making the summary longer.
//
// elsewhereScale is what Elsewhere's gains are worth after its cap, so 5,000
// PRs merged elsewhere don't outrank the 24 points earned here when they add
// only 20 (Copilot on #30).
func reason(cs []Component, earned, elsewhereScale float64) string {
	var cost, gain *Component
	var best float64
	for i := range cs {
		c := &cs[i]
		if c.why == "" {
			continue
		}
		if c.Points <= reasonMinCost && -c.Points >= reasonShare*earned && (cost == nil || c.Points < cost.Points) {
			cost = c
		}
		worth := c.Points
		if c.Group == groupElsewhere {
			worth *= elsewhereScale
		}
		if c.Points > 0 && worth > best {
			gain, best = c, worth
		}
	}
	switch {
	case cost != nil:
		return cost.why
	case gain != nil:
		return gain.why
	}
	return "no activity here or elsewhere"
}

// monthsSpan describes a span of days in months, at least 1.
func monthsSpan(days int) string {
	return plural(max(1, int(math.Round(float64(days)/daysPerMonth))), "month")
}

// engagement describes a count of issues, threads, and reviews.
func engagement(n int) string {
	if n == 0 {
		return "no issues, comments, or reviews"
	}
	if n == 1 {
		return "1 issue, comment, or review"
	}
	return fmt.Sprintf("%s issues, comments, and reviews", thousands(n))
}

func acceptedDetail(g Global) string {
	if g.AcceptedSampled == 0 {
		return ""
	}
	return fmt.Sprintf("%s merged · %.0f%% accepted by others", thousands(g.MergedElsewhere), g.AcceptedShare()*100)
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
