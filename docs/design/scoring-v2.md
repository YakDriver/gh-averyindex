# Scoring v2: three scores, one line

Status: implemented. It builds on [philosophy.md](philosophy.md) and [tuning.md](tuning.md) and replaces the flags and tiers from v0.6.

## The problem

A1 opened 41 PRs here in 36 days. 37 are open, 3 were closed (two incorrect, one duplicate), and 1 merged: a 4-line doc fix. A1 has opened no issues, commented on no threads, and reviewed no PRs. averyindex v0.6 gives A1 a 32, "Contributor," in 49 lines with no flag.

That fails the tool's purpose in two ways:

- **The score misleads.** A1 gets the full 20 for Elsewhere, mostly from 2023 team-project repos with 0 to 50 stars. Not-landing costs only -1.9, because open PRs are neutral. Nothing measures the review A1 is asking for.
- **The output is too long.** A maintainer running it 200 times a day reads the header and skips the rest. Each flag we add is one more thing to interpret, and the output grows with every pattern we learn to detect.

## Philosophy changes

**Add: engagement is the price of review.** Every PR asks maintainers for review time. Two things earn that time: PRs that landed, and engagement such as issues, comments on other people's threads, and reviews. Asking for far more review than you've earned is a cost to maintainers, whether or not the PRs turn out to be good.

**Revise "open PRs are neutral."** A few open PRs, or many from someone with a record here, still cost nothing. That keeps the slow review queue from counting against contributors. Open and closed PRs count against someone only to the extent they outrun that person's merges and engagement.

**Revise "flags state facts and don't change the score."** If a fact matters to the decision, it belongs in the score. If it doesn't, it belongs in `--explain`. There are no flags.

**Pushback: no engagement isn't a minus by itself.** 9 of 50 scored authors have merged here with no issues, comments, or reviews. They include one-PR newcomers whose PRs were fine. Penalizing the absence would hit them. The imbalance is what costs: many PRs asked for, little earned.

## Design

### Three scores

averyindex is the sum of three scores, each shown and each rounded, so the line adds up:

| Score | Measures | Range |
|---|---|---|
| **Here** | What the user has given this repo: code credit, upvotes, reviews, threads, and issues | 0 and up |
| **Elsewhere** | The user's record outside this repo: benefit of the doubt (capped at 20), minus what that record costs | Up to 20 |
| **Burden** | Review the user has asked for here and not earned | 0 and down |

Each score is about one place. The costs of a user's record elsewhere sit in Elsewhere, so a user who asks a lot of other repos without giving back loses the benefit of the doubt there:

- **Not landing** (last 30 days) and **spray** (days 31 to 180), moved from Pattern.
- **Review not yet earned elsewhere:** PRs opened in other repos in the last 30 days and still open, beyond what the user earned there in the same window. Only open PRs count, because not-landing already weighs the closed ones.

Burden collects the costs here:

- **PRs closed unmerged** here, moved from Here.
- **Heavy maintainer involvement:** Here counts heavy PRs at the usual credit and Burden takes the difference back, so the cost is a visible line.
- **Review not yet earned:**

```
unearned = max(0, open + closed unmerged − 3 × merged − (issues + threads + reviews) − 3)
cost     = −4 × log2(1 + unearned)
```

Each merge earns review for 3 more PRs, and each act of engagement earns 1. The first 3 PRs are free. The free 3 came from the first test. Without them, three comparison authors lost 6 to 8 points: two with 1 merge and 5 or 6 PRs waiting here, and one with 3 PRs open elsewhere and nothing merged there yet.

There are no tiers. They added a label to learn on top of the number, and the three parts say more.

### Output

The default output is two lines. The second line has the three parts and the biggest factor:

```
someone in hashicorp/terraform-provider-aws  ·  ➜ averyindex -6
  Here +20  ·  Elsewhere +1  ·  Burden -27  ·  41 PRs here, 1 merged, no issues, comments, or reviews
```

- **The reason is generated, not detected.** Each component carries a short phrase. The reason is the biggest cost when it's at least 3 points and at least a quarter of what the user earned. Otherwise, it's the biggest source of points. That keeps "101 PRs here closed unmerged" from being the story for someone with 1,686 merged.
- **`--explain`** prints the full report. `--json` has everything, including `here`, `elsewhere`, `burden`, and `reason`. `schema_version` is 4.

## Results (50 authors, v0.6 and v2 on the same day, 2026-11-09)

| Author | v0.6 | v2 | Here / Elsewhere / Burden | Reason |
|---|---|---|---|---|
| A1 | 32 | −6 | 20 / 1 / −27 | 41 PRs here, 1 merged, no issues, comments, or reviews |
| Open-PR author | 28 | 22 | 12 / 19 / −9 | 15 PRs merged elsewhere |
| YakDriver | 246 | 246 | 246 / 20 / −20 | 1,686 PRs merged here |
| T1 | 78 | 78 | 71 / 20 / −13 | 14 PRs merged here |
| T2, T3, T4 | 21, 25, 23 | same | | 1 merged PR here needed heavy maintainer work |
| S1 | 21 | 22 | 21 / 4 / −3 | PRs closed unmerged in 29 repos elsewhere |
| S2 | 1 | 1 | 0 / 4 / −3 | PRs closed unmerged in 37 repos elsewhere |

Every other author moved by 1 point or less, from rounding. That covers 28 comparison authors, 5 maintainers, and 8 tracked authors. Before the free 3, two more comparison authors dropped 6 and 8.

The v0.6 penalties were already in place, so v2 mainly changes how the costs read, not what the tracked authors score. The new score change is for A1's pattern. A1 also now loses Elsewhere's benefit of the doubt, 20 down to 1, through 28 PRs still open elsewhere in 30 days.

## Open questions

- **Elsewhere for thin records.** Review not yet earned elsewhere covers someone asking a lot of other repos right now. Two gaps remain. Account age credits an account that sat idle for years the same as one that was active. Merges in tiny team-project repos count like any others. A1 got 20 from those until the open PRs elsewhere took it back. Options: credit only months with activity, or weight merges by the repo's reach. Both need their own test.
- **Weights.** The values 3, 1, 4, and the free 3 were fit to one case (A1) and checked against 49 others. Rerun the check as vouch requests come in.
- **Several users per run (#10).** Deferred.

## Checking it

Recompute the results table with v0.6 and the new build on the same day, and look at every author who moves by more than 1 point. The script and data stay private because they name people.
