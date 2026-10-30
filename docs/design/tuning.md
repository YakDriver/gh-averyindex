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
| H2 | Spraying is many repos with low landing, whatever the account age. | Newest 100 PRs in 180 days, 30+ repos, under 70% merged (counting only merges by someone else): flags S1 and S2, plus U1 and U2. Flags 0/4 maintainers and not YakDriver. Misses S3, whose newest 100 PRs are in one org. | **Adopt.** |
| H2a | YakDriver looks like a sprayer. | No. 154 PRs went to 2 repos, with 96% merged. PRs to repos YakDriver owns are already excluded from these numbers, and they count only through stars. | No change. |
| H3 | Line-based rework is noise for small PRs, so add a size floor. | The noise came from bot PRs: commit logins like `dependabot[bot]` didn't match the author. With bots removed, a floor barely helps (24 to 22 of 96 PRs). Those 24 PRs did get real maintainer rework. | **Reject.** Keep line rework. |
| H4 | PRs that maintainers close fast here signal slop. | Closed by a maintainer within 7 days: tracked 6/18 authors, comparison 4/28. This is partly circular, because closing is often why a PR got tracked. | Weak. Prefer a label (see open questions). |
| H5 | The 30-day burst flag catches sprayers. | It misses S1, whose 204 PRs to 94 repos came in June, on an account more than 5 years old. | Replaced by H2. |
| H6 | The tool can catch slop that looks clean. | T5's PR didn't compile, and a maintainer fixed it in 1 commit. It had 0 threads. Nothing here separates it. | Accept the miss. |

17 of the 18 tracked authors have 0 or 1 merged PRs here. Without a vouch, the gate stops them, and no score separates them from new good contributors. The costly cases were the 5 merged PRs. Their authors are the ones most likely to be vouched anyway, so H1b and H2 are aimed at them.

## Changes

1. **Maintainer-rewritten PRs (H1b).** For up to 100 merged PRs here, fetch `reviewThreads.totalCount`. For PRs with 20+ threads, fetch commits and count non-merge commits by others. If there are 10 or more, give that PR 0 credit, subtract 15 points per PR (at most -45), and flag it as "Maintainers rewrote N merged PRs." Cost: 1 or 2 more GraphQL requests.
   - Accept if T1 to T4 get the flag, T1 drops below Veteran (80), and G1 has no flag.
2. **Spray flag (H2).** Add one item search to the batch: the newest 100 public PRs in 180 days, outside the user's own repos. If they span 30+ repos and under 70% of resolved PRs were merged by someone else, subtract 20 points and flag "Spread across N repos, M% landed." This replaces `new-account-burst`. Cost: 1 alias in the existing batch.
   - Accept if S1 and S2 get the flag, and YakDriver and the 4 maintainers don't.
3. **Keep line rework as is (H3).** Use the recent sample to judge credit, and use H1b for the worst PRs.
4. **Bump `schema_version` to 3.** The `flags` codes change.

## Open questions

- **A maintainer label.** If maintainers label PRs they close for quality, a count of labeled PRs is the strongest signal available, and it's cheap to fetch. Should we add a label?
- **U1 and U2.** Both match the spray rule, and both have 52 open PRs. Before shipping change 2, a maintainer should read 3 PRs from each to confirm.
- **Self-merges in orgs.** S3 made 849 PRs in one org with 76% self-merged, and those count toward "Elsewhere." Excluding self-merges would also drop maintainer credit for hashicorp work. Maintainers are vouched anyway, but the question stays open.
- **AI-written review comments.** S1 posted 6,000-character reviews on other people's PRs here, and those count as positive threads. This hasn't been tested yet. It would need comment length or a maintainer label.

## Redo the analysis

After changes, rescore every labeled author. Every "Accept if" above must hold. Repeat when the tracker gains 10 more entries.
