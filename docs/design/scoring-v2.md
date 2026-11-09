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

What we penalize is the pattern of an AI PR factory: many PRs, fast, with few merged and little else. The cost has three parts:

```
earned = merged + 0.25 × (issues + threads + reviews)
short  = max(0, 1 − earned / PRs)
volume = −5 × log2(1 + PRs − 3)
rate   = min(1, PRs per month / 10)
cost   = volume × short² × rate
```

- **Ratio (`short`).** For each PR, the user should have about one merge. An issue, comment, or review of someone else's work counts a quarter of a merge. It earns some review, but anyone can produce it alone (principle 3), and 40 "claiming this" comments are the cheapest thing an agent can do. At full weight, those 40 comments took A1's cost from −25 to 0. At a quarter, it's −14. Falling a little short costs little because the shortfall is squared.
- **Volume.** The first 3 PRs are free, and the cost grows with each doubling after that.
- **Rate.** Someone who opens a PR every month or two and does nothing else is authoring only, but that's an ordinary contributor. A factory opens dozens a month. The cost is full at 10 PRs a month and scales down below that. Here, the rate is the higher of PRs per month since the user's first issue or PR here and PRs per month in the last 90 days. The second keeps one old issue from shielding an account that comes back and sprays. Without it, the same 41 PRs cost −25 over 1 month and −3 after an issue 3 years earlier.

The same cost appears twice. **Burden** covers this repo. **Elsewhere** has a matching line for the last 30 days in other repos. It counts only PRs still open, with no merges in the ratio, because **not landing** already covers the PRs that resolved. That way no PR is charged twice. Comments and reviews elsewhere are read by their own timestamps, page by page through the window. A thread the user commented on and reviewed counts once, and reviews of their own PRs don't count.

Burden also has **heavy maintainer involvement**: Here counts heavy PRs at the usual credit, and Burden takes the difference back, so the cost is a visible line.

Elsewhere's other costs are **not landing** (last 30 days) and **spray** (days 31 to 180).

There's no separate penalty for PRs closed unmerged. A first version had one, and it gave YakDriver a Burden of −20 for 101 closed PRs among 1,686 merged and more than 2,000 issues, comments, and reviews. Closed PRs now cost something only through the ratio, so they matter for someone who authors without merging.

There are no tiers. They added a label to learn on top of the number, and the three parts say more.

### Output

The default output is two lines. The second line has the three parts and the biggest factor:

```
someone in hashicorp/terraform-provider-aws  ·  ➜ averyindex -8
  Here +20  ·  Elsewhere -3  ·  Burden -25  ·  41 PRs here, 1 merged, no issues, comments, or reviews
```

- **The reason is generated, not detected.** Each component carries a short phrase. The reason is the biggest cost when it's at least 3 points and at least a quarter of what the user earned. Otherwise, it's the biggest source of points, with Elsewhere's gains ranked after the cap. Two exceptions:
  - A drive-by, with PRs here and Here at 0, gets its PRs as the reason ("3 PRs here, none merged, no issues, comments, or reviews"), not its account's age.
  - PRs merged elsewhere show the share someone else accepted when it's under 90%, so a self-merge farm reads "1,645 PRs merged elsewhere, 21% accepted by others."
- **`--explain`** prints the full report. `--json` has everything, including `here`, `elsewhere`, `burden`, and `reason`. `schema_version` is 4.

## Results (50 authors, v0.6 and v2, 2026-11-09)

| Author | v0.6 | v2 | Here / Elsewhere / Burden | Reason |
|---|---|---|---|---|
| A1 | 32 | −8 | 20 / −3 / −25 | 41 PRs here, 1 merged, no issues, comments, or reviews |
| S3 | 14 | −13 | 6 / −18 / −1 | 495 PRs elsewhere in 30 days still open, 210 issues, comments, and reviews |
| Scheme example | −1 | −19 | 0 / −19 / 0 | 76 PRs elsewhere in 30 days still open, 27 issues, comments, and reviews |
| YakDriver | 246 | 266 | 246 / 20 / 0 | 1,686 PRs merged here |
| Maintainers (4) | 221 to 293 | 232 to 312 | Burden 0 | their merges |
| T1 | 78 | 89 | 71 / 20 / −2 | 14 PRs merged here |
| T2, T3, T4 | 21, 25, 23 | 24, 25, 23 | Burden −12 each | 1 merged PR here needed heavy maintainer work |
| S1, S2 | 21, 1 | 28, 5 | | PRs closed unmerged in 29 and 36 repos elsewhere |

- **Five authors drop more than 2 points,** each because of an authoring cost of at least 4. A1, S3, and the scheme example drop 18 to 40. A comparison author with 40 PRs in a month across 11 repos drops 16, to 21. A good comparison author drops 4, to 73, for 26 PRs here in 90 days with 9 of 36 merged.
- **Authors who only author, slowly,** pay little. The author with 9 PRs and 1 merge over 6 months gets Burden −2.
- **The rest move between −2 and +20, and 28 gain.** The closed-unmerged penalty is gone, and it was largest for people with many PRs. That includes some tracked authors: T1 gains 11, and S1 and S2 gain 4 to 7. Heavy maintainer involvement and spray still mark them in their reason lines. It's a deliberate trade toward penalizing the factory pattern, and it should be checked as vouch requests come in.

## Open questions

- **Elsewhere for thin records.** Authoring without engagement elsewhere covers someone sending other repos a lot of PRs right now. Two gaps remain. Account age credits an account that sat idle for years the same as one that was active. Merges in tiny team-project repos count like any others. A1 got 20 from those until the open PRs elsewhere took it back. Options: credit only months with activity, or weight merges by the repo's reach. Both need their own test.
- **Weights.** The ratio target of 1, engagement at a quarter of a merge, the full rate of 10 a month, the weight of 5, and the free 3 were fit to A1 and the scheme example, then checked against 48 others. Rerun the check as vouch requests come in.
- **Slow review queues elsewhere.** A contributor with 83 PRs elsewhere in 30 days, 72 still open, and no comments or reviews costs about −31 in Elsewhere, even with a good record before that. That's the factory pattern by these numbers. v0.6 deliberately didn't flag it. Crediting the user's landing history would spare them, but it would also spare A1, whose history elsewhere lands 84%.
- **Several users per run (#10).** Deferred.

## Checking it

Recompute the results table with v0.6 and the new build on the same day, and look at every author who moves by more than 1 point. The script and data stay private because they name people.
