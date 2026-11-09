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
| **Burden** | What the user costs maintainers here | 0 and down |

Each score is about one place. Elsewhere leaves out the scored repo, so nothing here counts twice.

### Authoring without engagement

What we penalize is the pattern of an AI PR factory: many PRs, fast, with few merged and little else. Someone who reviews and discusses as much as they author pays nothing, however many PRs they've opened or had closed. The cost has three parts:

```
short  = max(0, 1 − (merged + issues + threads + reviews) / PRs opened)
volume = −5 × log2(1 + PRs opened − 3)
rate   = min(1, PRs per month / 10)
cost   = volume × short² × rate
```

- **Ratio (`short`).** For each PR, the user should have about one merged PR or one act of engagement. Falling a little short costs little because the shortfall is squared.
- **Volume.** The first 3 PRs are free, and the cost grows with each doubling after that.
- **Rate.** Someone who opens a PR every month or two and does nothing else is authoring only, but that's an ordinary contributor. A factory opens dozens a month. The cost is full at 10 PRs a month and scales down below that. Here, the rate is PRs over the months since the user's first issue or PR. Elsewhere, it's the last 30 days.

The same cost appears twice. **Burden** covers this repo. **Elsewhere** has a matching line for the last 30 days in other repos, using comments and reviews by their own timestamps.

Burden also has **heavy maintainer involvement**: Here counts heavy PRs at the usual credit, and Burden takes the difference back, so the cost is a visible line.

Elsewhere's other costs are **not landing** (last 30 days) and **spray** (days 31 to 180).

There's no separate penalty for PRs closed unmerged. A first version had one, and it gave YakDriver a Burden of −20 for 101 closed PRs among 1,686 merged and more than 2,000 issues, comments, and reviews. Closed PRs now cost something only through the ratio, so they matter for someone who authors without engaging.

There are no tiers. They added a label to learn on top of the number, and the three parts say more.

### Output

The default output is two lines. The second line has the three parts and the biggest factor:

```
someone in hashicorp/terraform-provider-aws  ·  ➜ averyindex -6
  Here +20  ·  Elsewhere -1  ·  Burden -25  ·  41 PRs here, 1 merged, no issues, comments, or reviews
```

- **The reason is generated, not detected.** Each component carries a short phrase. The reason is the biggest cost when it's at least 3 points and at least a quarter of what the user earned. Otherwise, it's the biggest source of points. That keeps "101 PRs here closed unmerged" from being the story for someone with 1,686 merged.
- **`--explain`** prints the full report. `--json` has everything, including `here`, `elsewhere`, `burden`, and `reason`. `schema_version` is 4.

## Results (50 authors, v0.6 and v2, 2026-11-09)

| Author | v0.6 | v2 | Here / Elsewhere / Burden | Reason |
|---|---|---|---|---|
| A1 | 32 | −6 | 20 / −1 / −25 | 41 PRs here, 1 merged, no issues, comments, or reviews |
| Scheme example | −1 | −9 | 0 / −9 / 0 | 32 of 79 PRs elsewhere resolved in 30 days merged |
| YakDriver | 246 | 266 | 246 / 20 / 0 | 1,686 PRs merged here |
| Maintainers (4) | 221 to 293 | 232 to 312 | Burden 0 | their merges |
| T1 | 78 | 89 | 71 / 20 / −2 | 14 PRs merged here |
| T2, T3, T4 | 21, 25, 23 | 24, 25, 23 | Burden −12 each | 1 merged PR here needed heavy maintainer work |
| S1, S2 | 21, 1 | 25, 4 | | PRs closed unmerged in 29 and 36 repos elsewhere |

- **A1 and the scheme example** are the only authors who drop more than 2 points. Both open dozens of PRs a month with little else.
- **Authors who only author, slowly,** pay little. The author with 9 PRs and 1 merge over 6 months gets Burden −2.
- **Most authors gain 3 to 20 points.** The closed-unmerged penalty is gone, and it was largest for people with many PRs. That includes the tracked authors: T1 gains 11, S1 and S2 gain 3 or 4. Heavy maintainer involvement and spray still mark them in their reason lines. It's a deliberate trade toward penalizing the factory pattern, and it should be checked as vouch requests come in.

## Open questions

- **Elsewhere for thin records.** Authoring without engagement elsewhere covers someone sending other repos a lot of PRs right now. Two gaps remain. Account age credits an account that sat idle for years the same as one that was active. Merges in tiny team-project repos count like any others. A1 got 20 from those until the open PRs elsewhere took it back. Options: credit only months with activity, or weight merges by the repo's reach. Both need their own test.
- **Weights.** The ratio target of 1, the full rate of 10 a month, the weight of 5, and the free 3 were fit to A1 and the scheme example, then checked against 48 others. Rerun the check as vouch requests come in.
- **Several users per run (#10).** Deferred.

## Checking it

Recompute the results table with v0.6 and the new build on the same day, and look at every author who moves by more than 1 point. The script and data stay private because they name people.
