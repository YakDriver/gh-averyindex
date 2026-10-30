# Tuning the score (#13)

## Principle

Catching bad actors matters more than flattering solid contributors. When a signal costs a good contributor a few points but flags someone who sprays PRs or needs heavy rework, take it.

## Data (2026-10-30)

- **Tracked:** 19 PRs that maintainers logged as AI slop in terraform-provider-aws, from 18 authors. 14 were closed. 5 were merged, and those 5 took weeks of maintainer time.
- **Comparison:** 96 merged human PRs here from 2026 (48 authors), bots removed. It's unlabeled, so it may contain slop the tracker missed.
- **Spray data:** each author's public PRs from May to October 2026, outside repos they own. That covers the 18 tracked authors, 28 comparison authors, 4 maintainers, and YakDriver.

People are referred to by label. The raw data and the label key stay outside this repo.

## Hypotheses

| # | Hypothesis | Result | Decision |
|---|---|---|---|
| H1 | Merged slop leaves a trail of review threads. | PRs with 20+ review threads: comparison 0/96, tracked 4/5. Across every merged PR per author: tracked 4/10 authors, comparison 1/28 (G1). | Keep, revised by H1b. |
| H1b | Heavy review means slop only when maintainers end up writing the code. | 20+ threads **and** 10+ maintainer commits: comparison 0/96, tracked 4/5. G1 had 21 threads, but did all 63 commits itself. | **Adopt.** |
| H2 | Spraying is many repos with low landing, whatever the account age. | An all-or-nothing rule (30+ repos, under 70% landed) flags S1, S2, U1, and U2. It flags no maintainers and not YakDriver. Its cliff treats 29 repos the same as 2. | Revised by H2b. |
| H2b | Spray is the number of repos a PR went to without landing, and the penalty should grow faster than linearly. | "Unlanded repos" in the newest 100 PRs from 180 days: repos with no merged PR, and at least one PR closed or open more than 30 days. S2 66, S1 39, U1 33, U2 22. No other author is above 8. Maintainers and YakDriver are at 0 or 1. There's no data between 8 and 22, so the middle of the curve is untested. | **Adopt.** |
| H3 | Line-based rework is noise for small PRs, so add a size floor. | The noise came from bot PRs: commit logins like `dependabot[bot]` didn't match the author. With bots removed, a floor barely helps (24 to 22 of 96 PRs). Those 24 PRs did get real maintainer rework. | **Reject.** Keep line rework. |
| H4 | PRs that maintainers close fast here signal slop. | Closed by a maintainer within 7 days: tracked 6/18 authors, comparison 4/28. This is partly circular, because closing is often why a PR got tracked. | Weak. Prefer a label (see open questions). |
| H5 | The 30-day burst flag catches sprayers. | It misses S1, whose 204 PRs to 94 repos came in June, on an account more than 5 years old. | Replaced by H2. |
| H6 | The tool can catch slop that looks clean. | T5's PR didn't compile, and a maintainer fixed it in 1 commit. It had 0 threads. Nothing here separates it. | Accept the miss. |
| H7 | Self-merged PRs show nothing about whether others accept your code. | S3 has 849 PRs in one org, 76% self-merged, and that maxes out "Elsewhere." Maintainers self-merge 95 to 100% of their hashicorp PRs. | **Adopt.** Self-merges are neutral, neither credit nor spray. |
| H8 | Library authors earn trust through adoption, which stars miss. | One code search (`"github.com/YakDriver/" filename:go.mod -user:YakDriver`) finds 77 go.mod files in 29 repos from 25 owners, including pulumi, crossplane-contrib, upbound, and hashicorp. regexache has 7 stars. S1 and S2 have 0. | **Adopt** for Go. Other ecosystems are open. |

17 of the 18 tracked authors have 0 or 1 merged PRs here. Without a vouch, the gate stops them, and no score separates them from new good contributors. The costly cases were the 5 merged PRs. Their authors are the ones most likely to be vouched anyway, so H1b and H2 are aimed at them.

## Changes

1. **Maintainer-rewritten PRs (H1b).** For up to 100 merged PRs here, fetch `reviewThreads.totalCount`. For PRs with 20+ threads, fetch commits and count non-merge commits by others. If there are 10 or more, give that PR 0 credit, subtract 15 points per PR (at most -45), and flag it as "Maintainers rewrote N merged PRs." Cost: 1 or 2 more GraphQL requests.
   - Accept if T1 to T4 get the flag, T1 drops below Veteran (80), and G1 has no flag.
2. **Spray penalty (H2b).** Add one item search to the batch: the newest 100 public PRs in 180 days, outside the user's own repos, with `mergedBy`. Count unlanded repos `U`. Self-merged PRs don't count as landed or unlanded. Penalty: `-min(40, 2 × (2^(U/6) - 1))`. That's -2 at 6 repos, -4 at 10, -9 at 15, -18 at 20, -34 at 25, and -40 at 30 and above. Flag it at 15 or more: "PRs went unlanded in N repos." This replaces `new-account-burst`.
   - Accept if S1, S2, U1, and U2 get 18 or more points off, every other author loses 3 or fewer, and YakDriver and the maintainers lose less than 1.
3. **Only merges by others earn "Elsewhere" (H7).** Scale merged-elsewhere by the share of the 100-PR sample merged by someone else.
   - Accept if S3 drops to 55% of its current "Elsewhere" or less. It's fine if maintainers drop too, because they're vouched.
4. **Adoption (H8).** Run one REST code search per run: `"github.com/LOGIN/" filename:go.mod -user:LOGIN`. Score distinct owners on a log scale, capped at 15 points and shown under "Own projects." That's one call against a limit of 10 per minute.
   - Accept if YakDriver gets close to the cap, and S1 and S2 get 0.
5. **Keep line rework as is (H3).** Use the recent sample to judge credit, and use H1b for the worst PRs.
6. **Bump `schema_version` to 3.** The `flags` codes and the meaning of merged-elsewhere change.

## Open questions

- **A maintainer label.** If maintainers label PRs they close for quality, a count of labeled PRs is the strongest signal available, and it's cheap to fetch. Should we add a label?
- **U1 and U2.** Both match the spray rule, and both have 52 open PRs. Before shipping change 2, a maintainer should read 3 PRs from each to confirm.
- **Faking adoption.** Sock-puppet accounts could import a module. The log scale and the 15-point cap limit the gain. Counting only owners with their own stars or activity would make it harder to fake.
- **Other ecosystems.** npm, PyPI, and others don't put the GitHub login in the dependency name. deps.dev could map packages to repos.
- **AI-written review comments.** S1 posted 6,000-character reviews on other people's PRs here, and those count as positive threads. This hasn't been tested yet. It would need comment length or a maintainer label.

## Redo the analysis

After changes, rescore every labeled author. Every "Accept if" above must hold. Repeat when the tracker gains 10 more entries.
