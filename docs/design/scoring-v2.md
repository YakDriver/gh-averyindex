# Scoring v2: three scores, one line

Status: proposal. It builds on [philosophy.md](philosophy.md) and [tuning.md](tuning.md) and changes parts of both.

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

averyindex is the sum of three scores, each shown:

| Score | Measures | Range |
|---|---|---|
| **Here** | What the user has given this repo: code credit, upvotes, reviews, threads, and issues | 0 and up |
| **Elsewhere** | Benefit of the doubt from work outside this repo | 0 to 20 |
| **Burden** | Review the user has asked for and not earned | 0 and down |

**Burden** collects every cost: PRs closed unmerged here, not-landing (last 30 days), spray (days 31 to 180), heavy maintainer involvement (as lost credit), and the new unearned-review term:

```
unearned = max(0, open + closed unmerged here − 3 × merged here − engagement here)
cost     = −4 × log2(1 + unearned)
```

Here, engagement is issues + threads + reviews. Each merge earns review for 3 more PRs, and each act of engagement earns 1.

### Output

The default output is one line, plus a second line naming the biggest factor:

```
A1  ·  Novice  ·  ➜ averyindex 11
  Here +20  ·  Elsewhere +20  ·  Burden −29: 40 PRs here, 1 merged, no issues, comments, or reviews
```

- **The reason is generated, not detected.** It describes the largest Burden component. When Burden is near 0, it describes the largest source of points instead, such as "1,686 merged here." New components feed the same reason, so the output never grows.
- **`--explain`** prints today's full report.
- **`--json`** is unchanged except that flags are removed. Bump `schema_version` to 4. No one depends on the format yet.

## First test (50 authors, data from 2026-10-30 and 2026-11-09)

Adding only the unearned term to v0.6 changes 7 of 50 scores:

| Author | v0.6 | With the term | Tier | Why |
|---|---|---|---|---|
| A1 | 32 | 11 | Contributor → Novice | 40 PRs asked, 1 merged, no engagement |
| Open-PR author | 28 | 18 | Contributor | 8 asked, 1 merged, no engagement |
| S3 | 16 | 8 | Contributor → Novice | 7 closed, nothing merged |
| Comparison author | 44 | 36 | Regular → Contributor | 6 open, 1 merged, no engagement |
| Comparison author | 33 | 27 | Contributor | 5 open, 1 merged, no engagement |
| Scheme example | −17 | −23 | Novice | |
| S2 | 1 | −3 | Novice | |

No maintainer, no veteran, and no author with a record of merges or engagement changes. The two comparison authors that drop are unlabeled one-merge authors, each with 5 or 6 PRs waiting and no engagement. Under the new principle, that's the intent.

## Open questions

- **Elsewhere for thin records.** A1's 20 comes from tiny team repos. Options: weight merges by the repo's reach, or limit Elsewhere when Here is near 0. This needs its own test.
- **Weights.** The values 3, 1, and 4 were fit to one case (A1) and checked against 49 others. Rerun the check when the tracker grows.
- **Tiers.** With Burden shown, are five tier names still needed, or is the score plus its reason enough?
- **Several users per run (#10).** At 200 runs a day, a table with one row per user would fit the one-line format.

## Plan

One PR: the Burden group with the unearned term, the flags removed (their facts become Burden components or `--explain` lines), the two-line default output with `--explain`, `schema_version` 4, and updates to the README and philosophy. Rerun the acceptance check and this table before merging.
