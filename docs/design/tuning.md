# Tuning the score (#13)

The principles are in [philosophy.md](philosophy.md). For tuning, the key one is principle 4: rework and spraying must be plain to see in the report. That means a line or flag that states the facts, with a bounded score effect, not a cut big enough to drop a real contributor a tier.

## Data (2026-10-30)

- **Tracked:** 19 PRs that maintainers logged as AI slop in terraform-provider-aws, from 18 authors. 14 were closed. 5 were merged, and those 5 took weeks of maintainer time.
- **Comparison:** 96 merged human PRs here from 2026 (48 authors), bots removed. It's unlabeled.
- **Elsewhere:** each author's public PRs from May to October 2026, outside repos they own. That covers 18 tracked authors, 28 comparison authors, 4 maintainers, and YakDriver. Approvals were fetched for 13 of them.

People are referred to by label. The raw data and the label key stay outside this repo. Every threshold below was picked after looking at all the data, so none of it is validated out of sample yet. New tracker entries are the holdout.

## Hypotheses

| # | Hypothesis | Result (n) | Decision |
|---|---|---|---|
| H1 | Merged slop leaves heavy review. | PRs with 20+ review threads: comparison 0/96 (max 18), tracked 4/5. Across all merged PRs: comparison authors 1/28 (G1, 21 threads). | Revised by H1b. |
| H1b | It's slop-like only when maintainers also wrote code. | 20+ threads and 10+ maintainer commits: comparison 0/96, tracked 4/5 (T1 to T4). G1 had 21 threads but made all 63 commits itself. The commit clause is fit to one case (G1). | **Adopt as a flag.** |
| H1c | Line rework can carry the flag. | T2 has 136 threads and 18 maintainer commits, but line rework of 0.07, because its own additions were huge. Keying the flag to line rework misses T2. | **Reject.** Key it to threads and maintainer commits, and word the flag as facts. |
| H2 | Spraying is many repos with low landing. | An all-or-nothing cutoff treats 29 repos the same as 2. | Revised by H2b. |
| H2b | Count repos where nothing landed, with a penalty that grows faster than linearly. | The first version counted open PRs older than 30 days. That conflicts with the rule that waiting PRs don't count. | Revised by H2c. |
| H2c | Same, but count closed-unmerged only, and saturate. | In the newest 100 PRs from 180 days, repos with a closed PR and nothing merged: S2 35, S1 29, U1 15, U2 11. Every other author is at 5 or below, and the maintainers and YakDriver are at 0 or 1. | Revised by H2e. |
| H2d | U1 and U2 are sprayers. | Read 12 of their closed PRs. Most were superseded, duplicates, or folded in with credit. One got "You are using an LLM rather uncritically." They're prolific and of mixed quality, not plain sprayers. | No flag for them, only a small cost. |
| H2e | Spray shouldn't double-charge the PRs that not-landing already counts. | A cap on Pattern would weaken obvious schemes: the README's example scheme account gets -25 from not-landing alone. Splitting the windows works instead. With spray over days 31 to 180, the counts are S2 36, S1 30, U1 15, U2 11, every other author 5 or below, and the maintainers and YakDriver 0 or 1. | **Adopt.** No Pattern cap. |
| H2f | Spray needs a flag. | A flag at any count is a cliff, which is the problem H2 had. | **Reject.** Spray gets a graded breakdown line. Flags stay for yes-or-no facts. |
| H3 | Line rework is noise for small PRs. | The noise was bot PRs. With bots removed, a size floor changes 24 to 22 of 96. | **Reject.** Keep line rework. |
| H4 | PRs that maintainers close fast signal slop. | Tracked 6/18 authors, comparison 4/28. Partly circular. | Weak. Use a label instead. |
| H5 | The 30-day burst flag catches sprayers. | It misses S1, whose burst came in June on a 5-year-old account. | Replaced by H2e. |
| H6 | Clean-looking slop is catchable. | T5 had 0 threads and 1 maintainer fix commit. | Accept the miss. |
| H7 | Who merged a PR shows whether others accepted it. | No. Maintainers self-merge 94 to 100% of their PRs, but 98 to 100% were approved by someone else. S3 self-merged 69% and had 4% approved. | **Reject.** |
| H7b | A merged PR shows acceptance when someone else merged it or approved it. | Maintainers and YakDriver at 98 to 100%. S3 at 33%: 31 merged by others, plus 2 self-merges that were approved. | **Adopt.** |
| H8 | Adoption shows trust for library authors that stars miss. | YakDriver's Go modules are imported by 25 owners, including pulumi, crossplane-contrib, and upbound. S1 and S2 have 0. But it's Go-only, misses org-hosted modules, and needs REST code search. | **Defer.** H7b keeps YakDriver's "Elsewhere," so nothing is lost meanwhile. |

17 of the 18 tracked authors have 0 or 1 merged PRs here. The gate stops them, and no score separates them from a good newcomer. The costly cases were the 5 merged PRs. One slop PR is a fact about that PR. For T1 it's 1 of 14 merged.

## Changes

1. **Heavy maintainer involvement (H1b, H1c).** For up to 100 merged PRs here, fetch `reviewThreads.totalCount`. For PRs with 20+ threads, fetch commits. When maintainers made 10+ non-merge commits, that PR earns 0 credit through the existing credit path, with no separate penalty. Flag it: "Heavy maintainer involvement on N merged PRs (#X: 28 review threads, 57 maintainer commits)." Cost: 1 or 2 GraphQL requests.
   - Accept if T1 to T4 are flagged, G1 and the comparison authors aren't, and T1 drops at most one tier.
2. **Spray (H2e, H2f).** `U` counts repos in the user's newest 100 public PRs created 31 to 180 days ago, outside their own repos, that have a closed-unmerged PR and nothing merged. Open PRs don't count. The penalty is a logistic curve shifted so 0 repos scores 0: `-15/(1+e^(-(U-20)/4))`. That's -1 at 11, -3 at 15, -7 at 20, -14 at 30, and -15 at most. It's its own Pattern line, "PRs closed unmerged in N repos (31 to 180 days)," with no flag and no Pattern cap. `new-account-burst` goes away, and not-landing still covers the last 30 days.
   - Accept if S1 and S2 lose 13 or more, U1 and U2 lose 4 or fewer, no comparison author drops a tier, and the README's scheme example scores no higher than today.
3. **Elsewhere counts what others accepted (H7b).** In a sample of merged PRs, fetch `mergedBy` and `reviews(states: APPROVED){totalCount}`. Scale merged-elsewhere by the share merged or approved by someone else, and show the share on the line. The score effect is small. Points are logarithmic, so cutting S3's 1,095 merges to a third removes about 6 points, and the 20-point Elsewhere cap absorbs that because age and stars already exceed it. The gain is that the report says what others accepted. Thin profiles built on self-merges lose the most, which is the intent.
   - Accept if S3's line shows about 33% accepted, and the maintainers and YakDriver show 98% or more and keep their Elsewhere points.
4. **Adoption (H8): deferred.** Revisit if auto-vouch needs another positive signal.
5. **Line rework stays as is (H3).**
6. **Bump `schema_version` to 3.** Flag codes change, and merged-elsewhere changes meaning.

## Implementation

Do it as one PR per change, in this order. Each PR keeps `make ci` green and updates the README sections it touches.

**A. Spray (change 2).**
- `metrics.go`: add an item search to the batch, `is:pr author:LOGIN -user:LOGIN is:public created:D180..D31`, newest 100. Store `SpraySampled` and `SprayUnlandedRepos` on `Global`.
- `github.go`: item searches need `state` for PRs. Add a `searchReq.Detail` option that selects a richer fragment (`state mergedBy { login } reviews(states: APPROVED) { totalCount }`), so other searches don't pay for those fields.
- `score.go`: a `Spray` component in Pattern using the curve above. Remove `new-account-burst` and its constants. Remove `SearchNext` paging if nothing else needs it, since it only existed for that flag. The "Last 30 days" report line stays, counted from the first page.
- Tests: the curve at 0, 11, 20, and 50; a repo with a merge doesn't count; open PRs don't count; the windows don't overlap.

**B. Elsewhere acceptance (change 3).**
- `metrics.go`: add a detailed item search, `is:pr author:LOGIN -user:LOGIN is:public is:merged -repo:REPO`, newest 100. Count PRs merged by someone else or approved. Store `ElsewhereAccepted` and `ElsewhereSampled`.
- `score.go`: "PRs merged elsewhere" uses `MergedElsewhere × accepted/sampled`, or `MergedElsewhere` when the sample is empty. The detail says "N merged · M% accepted by others."
- Tests: a self-merged unapproved PR doesn't count, a self-merged approved PR does, and a PR merged by someone else does.

**C. Heavy maintainer involvement (change 1).**
- `github.go`: `HeavyPRs(repo, login)` searches `repo:REPO is:pr is:merged author:LOGIN`, the first 100 by `updated-desc`, with `number reviewThreads { totalCount }`. For PRs with 20 or more threads, it fetches commits in one aliased request using `prFields`.
- `metrics.go`: a PR is heavy when 10 or more non-merge commits aren't owned by the user, using `ownedBy`. Logins ending in `[bot]` don't count. Store `HeavyPRs []{number, review_threads, maintainer_commits}`.
- `score.go`: `CodeCredit = (PRsMerged − heavy) × AvgCredit`, where AvgCredit leaves out heavy PRs that are in the recent sample, and is 1 when nothing is left. Add the flag `heavy-maintainer-involvement`, listing the worst PR.
- Tests: the 20-thread and 10-commit edges, own commits that don't count (G1's case), bot commits that don't count, and no double-counting when a heavy PR is in the recent sample.

**D. Release.** Bump `schema_version` to 3 with its change history: `new-account-burst` removed, `heavy-maintainer-involvement` added, merged-elsewhere weighted by acceptance, and the new fields. Run the acceptance check, then tag.

**Acceptance check.** `accept.py` and `baseline.json` sit with the private data. The baseline is v0.5.0's scores. The script rescores every labeled author with the local build and checks each "Accept if." Run it before merging each PR.

## Open questions

- **A maintainer label** for PRs closed for quality. It would be the strongest and cheapest signal available, and it would replace the threshold fitting in H1b. Add one to terraform-provider-aws?
- **Denounced accounts.** Should averyindex show them, or leave that to the vouch system? Either way, keep its inputs public and reproducible.
- **Noted, not designed around** (philosophy principle 1): faked adoption, sock-puppet approvals, and AI-written review comments, which S1 posted here and which count as positive threads today.

## Redo the analysis

After the changes, rescore every labeled author. Every "Accept if" must hold. When the tracker gains 10 entries, score them before looking at their data, and report the results with n.
