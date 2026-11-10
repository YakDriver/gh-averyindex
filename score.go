package main

import (
	"fmt"
	"math"
	"strings"
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

// Weights are hand-set, then checked against 98 terraform-provider-aws
// authors rated by reading their records without the score. About 7 in 10
// land in the band the rating picked: below 0 for a factory or spray, 0 to 14
// for not yet, 15 to 39 for look closer, 40 to 99 for a real record here, and
// 100 and up for a regular. GainOffset and the ThumbsUp weight were changed to
// fit those ratings. Randomly changing every weight by ±25% flips about 2% of
// pairwise orderings. Band edges aren't exact. See docs/design/scoring-v2.md.
//
// Weights are points per doubling of each count. Gains go through gain and
// costs through points, so the first few contributions matter most and huge
// counts don't drown out everything else.
//
// Signals that depend on someone else's judgment (a maintainer merged it and
// didn't rewrite it, other users upvoted it) weigh more than signals the user
// produces alone (comments, issues), because they're harder to inflate.
type Weights struct {
	// GainOffset shifts every gain's curve. See gain.
	GainOffset float64

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
	// AuthoringMaxRate caps the rate multiplier here: at AuthoringFullRate
	// PRs a month it's 1, and it keeps rising to AuthoringMaxRate. Elsewhere
	// it stops at 1, because a prolific author can have a big review
	// backlog across many repos without being a factory.
	AuthoringMaxRate float64
	EngagementCredit float64
}

var defaultWeights = Weights{
	GainOffset: 2,

	Code:     12,
	ThumbsUp: 2.5,
	Reviews:  5,
	Threads:  2,
	Issues:   2,

	AccountAge:      1.5,
	MergedElsewhere: 4,
	ProjectStars:    2,
	Contributors:    4,
	ElsewhereCap:    20,
	NotLanding:      -6,
	SprayMax:        -25,
	SprayMid:        20,
	SpraySteep:      4,

	Authoring:         -5,
	AuthoringTarget:   1,
	AuthoringFullRate: 10,
	AuthoringMaxRate:  2,
	EngagementCredit:  0.25,
}

// authoringGrace is how many PRs anyone can open before the balance between
// authoring and everything else counts.
const authoringGrace = 3

// authoringPoints is the cost of authoring without engagement, the pattern
// of an AI PR factory: many PRs, fast, with few merged and little else.
//
// It's a ratio. For each PR opened, the user should have about
// AuthoringTarget merged PRs. An issue, comment, or review of someone else's
// work counts EngagementCredit of a merge: it earns some review, but anyone
// can produce it alone, and 40 "claiming this" comments are the cheapest
// thing an agent can do (principle 3). The cost grows with how far short
// the user falls (squared, so falling a little short costs little) and with
// how many PRs they opened past the grace.
//
// It's also a rate. Someone who opens a PR every month or two and does
// nothing else is authoring only, but that's an ordinary contributor. A
// factory opens dozens a month. The rate multiplier is 1 at
// AuthoringFullRate PRs a month, smaller below that, and rises above it up
// to maxRate, so 35 a month costs more than 10.
func authoringPoints(prs, merged, gave int, perMonth, maxRate float64, w Weights) float64 {
	if prs <= authoringGrace {
		return 0
	}
	earned := float64(merged) + w.EngagementCredit*float64(gave)
	short := max(0, 1-earned/(w.AuthoringTarget*float64(prs)))
	return points(float64(prs-authoringGrace), w.Authoring) * short * short * min(maxRate, perMonth/w.AuthoringFullRate)
}

// hereRate is the user's PRs here per month: the higher of their rate since
// their first issue or PR here and their rate in the last recentHereDays.
// The recent rate keeps one old issue from shielding an account that comes
// back and sprays.
func hereRate(m Metrics) float64 {
	return max(hereLifetimeRate(m), float64(m.RecentPRs)/(recentHereDays/daysPerMonth))
}

// hereLifetimeRate is PRs here per month since the first issue or PR here,
// counting less than a month as a month.
func hereLifetimeRate(m Metrics) float64 {
	return float64(m.PRsOpened) / max(1, float64(m.ActiveDays)/daysPerMonth)
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

	// elsewhereGains and elsewhereCap let the full report show how much of
	// Elsewhere's benefit of the doubt counted, so a good record isn't
	// hidden behind a negative total.
	elsewhereGains, elsewhereCap float64
}

// gain is points for a gain: weight per doubling, with the curve shifted by
// GainOffset so the first few items count less. With an offset of 2, 1 merged
// PR is worth 5 points, 3 are 12, and 1,000 are 101. Without it, one 4-line
// merge and two upvotes made Here +20, which reads like a real record
// (calibration on 98 authors, 2026-11-10).
func gain(n, weight float64, w Weights) float64 {
	if n <= 0 {
		return 0
	}
	return weight * (math.Log2(1+n+w.GainOffset) - math.Log2(1+w.GainOffset))
}

// points is weight per doubling of n, for costs.
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
		{Label: "Merged PRs", Detail: fmt.Sprintf("%s merged × %.2f", thousands(m.PRsMerged), m.AvgCredit()),
			Count: gross, Points: gain(gross, w.Code, w), why: plural(m.PRsMerged, "PR") + " merged here"},
		{Label: "Upvotes received", Count: float64(m.ThumbsUp), Points: gain(float64(m.ThumbsUp), w.ThumbsUp, w),
			why: plural(m.ThumbsUp, "upvote") + " here"},
		{Label: "PRs reviewed", Count: float64(m.Reviews), Points: gain(float64(m.Reviews), w.Reviews, w),
			why: "reviewed " + plural(m.Reviews, "PR") + " here"},
		{Label: "Threads commented", Count: float64(m.Threads), Points: gain(float64(m.Threads), w.Threads, w),
			why: "commented on " + plural(m.Threads, "thread") + " here"},
		{Label: "Issues opened", Count: float64(m.Issues), Points: gain(float64(m.Issues), w.Issues, w),
			why: "opened " + plural(m.Issues, "issue") + " here"},
	}

	elsewhere := []Component{
		{Label: "Account age", Detail: age(g.AccountAgeDays), Count: months, Points: gain(months, w.AccountAge, w),
			why: "account " + age(g.AccountAgeDays) + " old"},
		{Label: "PRs merged elsewhere", Detail: acceptedDetail(g), Count: g.AcceptedMergedElsewhere(),
			Points: gain(g.AcceptedMergedElsewhere(), w.MergedElsewhere, w), why: mergedElsewhereWhy(g)},
		{Label: "Stars on own projects", Detail: fmt.Sprintf("%d repos with %d+", g.ProjectsStarred, minProjectStars),
			Count: float64(g.ProjectStars), Points: gain(float64(g.ProjectStars), w.ProjectStars, w),
			why: plural(g.ProjectStars, "star") + " on own projects"},
		{Label: "Contributors to own projects", Count: float64(g.Contributors),
			Points: gain(float64(g.Contributors), w.Contributors, w), why: plural(g.Contributors, "contributor") + " to own projects"},
	}
	var positive float64
	for _, c := range elsewhere {
		positive += c.Points
	}
	if positive > w.ElsewhereCap {
		elsewhere = append(elsewhere, Component{
			Label:  "Cap",
			Detail: fmt.Sprintf("%+.1f of gains counts as %+.0f", positive, w.ElsewhereCap),
			Points: w.ElsewhereCap - positive,
		})
	}
	// Not landing and authoring without engagement split the recent PRs
	// elsewhere so none is charged twice: not landing covers those that
	// resolved, and authoring covers those still open, waiting for review.
	miss := 1 - g.LandingRate()
	resolved := g.RecentResolved()
	gave := g.RecentGave()
	open := g.RecentOpen()
	authoringElsewhere := authoringPoints(open, 0, gave, float64(open)/(recentDays/daysPerMonth), 1, w)
	// When engagement couldn't be read, there's no cost: missing isn't
	// none. When there was more than was read, the cost still uses the count
	// read, and the text says it's a lower bound. Skipping the cost there
	// would excuse a self-merge farm whose comments are mostly in its own
	// repos: S3 would go from -12 to 24.
	gaveDetail, gaveWhy := thousands(gave)+" given", engagement(gave)
	switch {
	case g.EngagementUnchecked:
		authoringElsewhere, gaveDetail = 0, "comments and reviews unchecked"
	case g.EngagementTruncated:
		gaveDetail, gaveWhy = "at least "+gaveDetail, "at least "+gaveWhy
	}
	elsewhere = append(elsewhere,
		Component{
			Label: "Recent PRs not landing",
			Detail: fmt.Sprintf("%s in %d days · %s of %s merged · history %s of %s",
				thousands(g.RecentPRs), recentDays, thousands(g.RecentMerged), thousands(resolved),
				thousands(g.HistoryMerged()), thousands(g.HistoryResolved())),
			Count:  float64(resolved),
			Points: points(float64(resolved), w.NotLanding) * miss * miss,
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
			Label:  "Authoring without engagement elsewhere",
			Detail: fmt.Sprintf("%d days · %s open · %s", recentDays, thousands(open), gaveDetail),
			Count:  float64(open),
			Points: authoringElsewhere,
			why: fmt.Sprintf("%s elsewhere in %d days still open, %s", plural(open, "PR"),
				recentDays, gaveWhy),
		},
	)

	burden := []Component{
		{Label: "Heavy maintainer involvement", Detail: heavyDetail(m),
			Count: float64(len(m.HeavyPRs)), Points: gain(net, w.Code, w) - gain(gross, w.Code, w),
			why: plural(len(m.HeavyPRs), "merged PR") + " here needed heavy maintainer work"},
		{Label: "Authoring without engagement", Detail: fmt.Sprintf("%s · %.1f a month · %s merged · %s given",
			plural(m.PRsOpened, "PR"), hereRate(m), thousands(m.PRsMerged), thousands(m.Participation())),
			Count:  float64(m.PRsOpened),
			Points: authoringPoints(m.PRsOpened, m.PRsMerged, m.Participation(), hereRate(m), w.AuthoringMaxRate, w),
			why:    authoringWhy(m)},
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
	r.elsewhereGains, r.elsewhereCap = min(positive, w.ElsewhereCap), w.ElsewhereCap
	// Elsewhere's gains are ranked at what they're worth after the cap.
	scale := 1.0
	if positive > w.ElsewhereCap {
		scale = w.ElsewhereCap / positive
	}
	why, isCost := reason(r.Components, float64(r.Here)+min(positive, w.ElsewhereCap), scale)
	// A drive-by, with PRs here and nothing earned here, is explained by
	// that, not by an old account elsewhere.
	if !isCost && r.Here == 0 && m.PRsOpened > 0 {
		why = authoringWhy(m)
	}
	r.Reason = why
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
func reason(cs []Component, earned, elsewhereScale float64) (why string, isCost bool) {
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
		return cost.why, true
	case gain != nil:
		return gain.why, false
	}
	return "no activity here or elsewhere", false
}

// authoringWhy describes the user's PRs here, at the pace the authoring
// cost uses, and what else they gave: "41 PRs here in 5 weeks, 37 open, 1
// merged, no issues, comments, or reviews."
func authoringWhy(m Metrics) string {
	pace := plural(m.PRsOpened, "PR") + " here"
	if m.FirstAuthored != nil {
		pace += " in " + span(m.ActiveDays)
	}
	if recent := float64(m.RecentPRs) / (recentHereDays / daysPerMonth); m.ActiveDays > 2*recentHereDays && recent > hereLifetimeRate(m) {
		pace = fmt.Sprintf("%s here, %d in %d days", plural(m.PRsOpened, "PR"), m.RecentPRs, recentHereDays)
	}
	parts := []string{pace}
	if m.PRsOpen > 0 {
		parts = append(parts, thousands(m.PRsOpen)+" open")
	}
	merged := "none merged"
	if m.PRsMerged > 0 {
		merged = thousands(m.PRsMerged) + " merged"
	}
	return strings.Join(append(parts, merged, engagement(m.Participation())), ", ")
}

// span describes a stretch of days in the largest unit that reads
// naturally: days, weeks, months, or years.
func span(days int) string {
	days = max(1, days)
	switch {
	case days < 14:
		return plural(days, "day")
	case days < 60:
		return plural(int(math.Round(float64(days)/7)), "week")
	case days < 730:
		return plural(int(math.Round(float64(days)/daysPerMonth)), "month")
	}
	return fmt.Sprintf("%.1f years", float64(days)/365.25)
}

// mergedElsewhereWhy describes PRs merged elsewhere, with the share someone
// else accepted when it's low, so a self-merge farm doesn't read as 1,645
// PRs merged.
func mergedElsewhereWhy(g Global) string {
	why := plural(g.PRsMerged, "PR") + " merged elsewhere"
	if g.AcceptedSampled > 0 && g.AcceptedShare() < 0.9 {
		why += fmt.Sprintf(", %.0f%% accepted by others", g.AcceptedShare()*100)
	}
	return why
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
	return fmt.Sprintf("%s merged · %.0f%% accepted by others", thousands(g.PRsMerged), g.AcceptedShare()*100)
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
