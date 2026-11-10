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
| **Here** | What the user has given this repo: merged PRs, upvotes, reviews, threads, and issues | 0 and up |
| **Elsewhere** | The user's record outside this repo: benefit of the doubt (capped at 20), minus what that record costs | Up to 20 |
| **Burden** | What the user costs maintainers here | 0 and down |

Each score is about one place. Elsewhere leaves out the scored repo, so nothing here counts twice.

### Authoring without engagement

What we penalize is the pattern of an AI PR factory: many PRs, fast, with few merged and little else. The cost has three parts:

```
earned = merged + 0.25 × (issues + threads + reviews)
short  = max(0, 1 − earned / PRs)
volume = −5 × log2(1 + PRs − 3)
rate   = min(2, PRs per month / 10)    here; min(1, …) elsewhere
cost   = volume × short² × rate
```

- **Ratio (`short`).** For each PR, the user should have about one merge. An issue, comment, or review of someone else's work counts a quarter of a merge. It earns some review, but anyone can produce it alone (principle 3), and 40 "claiming this" comments are the cheapest thing an agent can do. At full weight, those 40 comments took A1's cost from −25 to 0. At a quarter, it's −14. Falling a little short costs little because the shortfall is squared.
- **Volume.** The first 3 PRs are free, and the cost grows with each doubling after that.
- **Rate.** Someone who opens a PR every month or two and does nothing else is authoring only, but that's an ordinary contributor. A factory opens dozens a month. The multiplier is 1 at 10 PRs a month and smaller below that. Here, it keeps rising to 2 at 20 a month, so 35 a month costs more than 10. Elsewhere, it stops at 1, because a prolific author can have a big review backlog across many repos without being a factory. One comparison author opened about 20 well-scoped PRs across five projects in a month, and a multiplier of 2 elsewhere would have cost him 13 points. Here, the rate is the higher of PRs per month since the user's first issue or PR here and PRs in the last 30 days. The second keeps one old issue from shielding an account that comes back and sprays. Without it, the same 41 PRs cost −25 over 1 month and −3 after an issue 3 years earlier.

The same cost appears twice. **Burden** covers this repo. **Elsewhere** has a matching line for the last 30 days in other repos. It counts only PRs still open, with no merges in the ratio, because **not landing** already covers the PRs that resolved. That way no PR is charged twice. Comments and reviews elsewhere are read by their own timestamps, page by page through the window. A thread the user commented on and reviewed counts once, and reviews of their own PRs don't count.

Burden also has **heavy maintainer involvement**: Here counts heavy PRs at the usual credit, and Burden takes the difference back, so the cost is a visible line.

Elsewhere's other costs are **not landing** (last 30 days) and **spray** (days 31 to 180).

There's no separate penalty for PRs closed unmerged. A first version had one, and it gave YakDriver a Burden of −20 for 101 closed PRs among 1,686 merged and more than 2,000 issues, comments, and reviews. Closed PRs now cost something only through the ratio, so they matter for someone who authors without merging.

There are no tiers. They added a label to learn on top of the number, and the three parts say more.

### Output

The default output is two lines. The second line has the three parts and the biggest factor:

```
someone in hashicorp/terraform-provider-aws  ·  ➜ averyindex -33
  Here +20  ·  Elsewhere -3  ·  Burden -50  ·  41 PRs here in 5 weeks, 37 open, 1 merged, no issues, comments, or reviews
```

- **The reason is generated, not detected.** Each component carries a short phrase. The reason is the biggest cost when it's at least 3 points and at least a quarter of what the user earned. Otherwise, it's the biggest source of points, with Elsewhere's gains ranked after the cap. Two exceptions:
  - When the reason is the user's PRs here, it states the pace and open count the cost is about: "41 PRs here in 5 weeks, 37 open, 1 merged, …"
  - A drive-by, with PRs here and Here at 0, gets its PRs as the reason ("3 PRs here, none merged, no issues, comments, or reviews"), not its account's age.
  - PRs merged elsewhere show the share someone else accepted when it's under 90%, so a self-merge farm reads "1,645 PRs merged elsewhere, 21% accepted by others."
- **`--explain`** prints the full report. Its Elsewhere header shows the gains that counted and the costs separately, "Elsewhere -3 (gains +20.0 of 20, costs -23.5)", so a good record isn't hidden behind a negative total. `--json` has everything, including `here`, `elsewhere`, `burden`, and `reason`. `schema_version` is 4.

## Results (50 authors, v0.6 and v2, 2026-11-10)

| Author | v0.6 | v2 | Here / Elsewhere / Burden | Reason |
|---|---|---|---|---|
| A1 | 32 | −33 | 20 / −3 / −50 | 41 PRs here in 5 weeks, 37 open, 1 merged, no issues, comments, or reviews |
| Scheme example | −1 | −19 | 0 / −19 / 0 | 76 PRs elsewhere in 30 days still open, 27 issues, comments, and reviews |
| S3 | 14 | −12 | 6 / −17 / −1 | 493 PRs elsewhere in 30 days still open, 210 issues, comments, and reviews |
| S2 | 1 | −4 | 0 / −4 / 0 | PRs closed unmerged in 36 repos elsewhere |
| S1 | 21 | 19 | 21 / −2 / 0 | PRs closed unmerged in 29 repos elsewhere |
| YakDriver | 246 | 266 | 246 / 20 / 0 | 1,686 PRs merged here |
| Maintainers (4) | 221 to 293 | 232 to 312 | Burden 0 | their merges |
| T1 | 78 | 89 | 71 / 20 / −2 | 14 PRs merged here |
| T2, T3, T4 | 21, 25, 23 | 24, 25, 23 | Burden −12 each | 1 merged PR here needed heavy maintainer work |

- **Everyone who drops more than 2 points does so because of an authoring or spray cost of at least 4.** A1, S3, and the scheme example drop 18 to 65. S2 and S1, the heavy sprayers, now have a negative Elsewhere, because spray reaches −25 and the gains stop at +20.
- **A good comparison author drops 13, to 64,** for 16 PRs here in the last 30 days with 9 of 36 merged and 24 open. They're well-scoped new resources and data sources, so that's a deserved signal about review load, not about quality. Most of the drop comes from measuring the recent pace over 30 days instead of 90. Doubling at 20 a month adds 7 of it.
- **Authors who only author, slowly,** pay little. The author with 9 PRs and 1 merge over 6 months gets Burden −2.
- **The rest move between −3 and +20, and most gain.** The closed-unmerged penalty is gone, and it was largest for people with many PRs. T1 gains 11. Heavy maintainer involvement and spray still mark the tracked authors in their reason lines.

The private check script also tests ordering pairs: A1, S2, and the scheme example rank below every good comparison author, every maintainer ranks above every tracked author, and A1 is among the 3 lowest.

## Calibration (98 authors, 2026-11-10)

Instead of tuning one author at a time, we rated a set of authors first and then checked the equations against the ratings.

- **Authors.** 48 from the comparison above, and 50 sampled from people who opened PRs in terraform-provider-aws since 2026-07-01, stratified by how their PRs went: only open, closed with none merged, a few merges, and 8 or more PRs.
- **Ratings.** Each author's record was read without the score: their newest PRs here and elsewhere, sizes, outcomes, who merged them, and engagement. Each got one of five bands, framed as a maintainer's workflow: **No** (below 0), **Not yet** (0 to 14), **Look** (15 to 39), **Yes** (40 to 99), and **Regular** (100 and up). Two passes were made, and the second resolved the disagreements.
- **One rule from the maintainer:** a record elsewhere isn't enough for Yes by itself. Under a vouch model, someone who can't open PRs can still open issues, comment, review, and upvote here. So Elsewhere stays capped at +20, below Yes.
- **Separation first.** The measure is how often an author scores at or below someone in a lower band, and how many land in their band. Roughly right is the goal, not matching each target.

What the ratings showed: the scores ran about one band high. Of 21 Not yet authors, 14 scored in Look. Thin records got too much from their first merge or first few upvotes. One 4-line merge and two upvotes made Here +20.

Two changes fixed most of that:

- **Gains are shifted so the first items count less.** Gains use `w × (log2(1 + n + 2) − log2(3))`. One merge is 5 points instead of 12, three are 12 instead of 24, and 1,000 are 101 instead of 120. Costs keep the plain curve.
- **👍 weighs 2.5, down from 5.** Upvotes from other accounts are easy to collect and, at 5, put authors with no merges into Look or Yes.

| | Before | After |
|---|---|---|
| Non-member authors in their band | 52 of 87 | 63 of 87 |
| Cross-band pairs out of order | 7.4% | 7.2% |
| Median score, Not yet / Look / Yes | 22 / 35 / 92 | 12 / 22 / 54 |
| A1 | −33 (Here +20) | −46 (Here +7) |

A free fit of all 18 weights did better on the authors it was fitted to, but no better under cross-validation (7.4% of pairs out of order, against 7.1% for the hand-set weights), so it wasn't adopted. Tying Elsewhere to activity here made the order worse (8% of pairs). Lowering the cap to 15 helped the order a little (6.8%) but put two fewer authors in their band, so the cap stays at 20.

**Org members score low when they work mostly outside the repo.** Of the 11 maintainers and staff, three score 31 to 55 because they've authored few PRs here. They don't need a vouch, so this matters only for how the number reads.

## Open questions

- **Elsewhere for thin records.** Authoring without engagement elsewhere covers someone sending other repos a lot of PRs right now. Two gaps remain. Account age credits an account that sat idle for years the same as one that was active. Merges in tiny team-project repos count like any others. A1 got 20 from those until the open PRs elsewhere took it back. Options: credit only months with activity, or weight merges by the repo's reach. Both need their own test.
- **Weights.** All weights are hand-set: the ratio target of 1, engagement at a quarter of a merge, the rate of 10 a month and the 2× cap here, the authoring weight of 5, the free 3, and the spray cap of −25. They were set against A1, S2, and the scheme example, then checked against 47 others. That's tuning and testing on the same set. Randomly changing every weight by ±25% flips about 1% of pairwise orderings, but A1's sign at −8 changed in about 1 draw in 6. Rerun the check, and add ordering pairs, as vouch requests come in.
- **Account age credits idle years.** Crediting only months with public activity would stop an old idle account from scoring like an active one. It needs the contributions calendar, so it's a separate change, and it's the next priority for thin records.
- **Slow review queues elsewhere.** A contributor with 83 PRs elsewhere in 30 days, 72 still open, and no comments or reviews costs about −31 in Elsewhere, even with a good record before that. That's the factory pattern by these numbers. v0.6 deliberately didn't flag it. Crediting the user's landing history would spare them, but it would also spare A1, whose history elsewhere lands 84%.
- **Several users per run (#10).** Deferred.

## Checking it

Recompute the results table with v0.6 and the new build on the same day, and look at every author who moves by more than 1 point. The script and data stay private because they name people.
