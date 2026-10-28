package main

import "math"

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
	Code           float64
	ThumbsUp       float64
	Reviews        float64
	Threads        float64
	Issues         float64
	ClosedUnmerged float64
}

var defaultWeights = Weights{
	Code:           12,
	ThumbsUp:       5,
	Reviews:        5,
	Threads:        2,
	Issues:         2,
	ClosedUnmerged: -3,
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
	Label  string  `json:"label"`
	Count  float64 `json:"count"`
	Points float64 `json:"points"`
}

// Result is the score and the components that produced it.
type Result struct {
	Components []Component `json:"components"`
	Score      int         `json:"averyindex"`
	Tier       string      `json:"tier"`
}

const codeCreditLabel = "Code credit"

func points(n, weight float64) float64 {
	if n <= 0 {
		return 0
	}
	return weight * math.Log2(1+n)
}

func score(m Metrics, w Weights) Result {
	parts := []struct {
		label  string
		count  float64
		weight float64
	}{
		{codeCreditLabel, m.CodeCredit(), w.Code},
		{"Upvotes received", float64(m.ThumbsUp), w.ThumbsUp},
		{"PRs reviewed", float64(m.Reviews), w.Reviews},
		{"Threads commented", float64(m.Threads), w.Threads},
		{"Issues opened", float64(m.Issues), w.Issues},
		{"PRs closed unmerged", float64(m.PRsClosedUnmerged()), w.ClosedUnmerged},
	}

	var r Result
	var total float64
	for _, p := range parts {
		pts := points(p.count, p.weight)
		total += pts
		r.Components = append(r.Components, Component{Label: p.label, Count: p.count, Points: pts})
	}
	r.Score = int(math.Round(total))
	r.Tier = tierFor(r.Score)
	return r
}
