# gh-averyindex

A [gh](https://cli.github.com/) extension that shows how helpful a user has been in a repository. It's meant for maintainers deciding whether to vouch for someone.

```console
$ gh averyindex johnthedev97
johnthedev97 in hashicorp/terraform-provider-aws  ·  Regular  ·  averyindex 49

Participation
  Issues opened      0
  Threads commented  6
  PRs reviewed       0
  Upvotes received   49
  First authored     PR #1183, 2017-07-19
  Last authored      PR #50684, 2026-10-27

Pull requests  3 merged · 1 open · 1 closed unmerged
  #19195  merged 2022-03-15  +153 -103  rework 288%  credit 0.00
  #9372   merged 2020-02-21  +1,975 -0  rework  12%  credit 0.88
  #9261   merged 2019-07-10  +349 -0    rework   0%  credit 1.00  (commits by crjohn)
  #50684  open since 2026-10-27

Score
  ...
```

The score is for a quick read, not a verdict. Look at the breakdown.

## Install

```sh
gh extension install YakDriver/gh-averyindex
```

## Usage

```sh
gh averyindex [-R owner/repo] [--json] <user>
```

Run it inside a clone to score against that repository, or pass `-R`. It uses your existing `gh` login, so it works on any repository you can read.

## The report

**Participation** is everything besides the user's own PRs: issues opened, other people's threads they commented on (threads, not comments, so fifty "+1"s on one issue count once), PRs they reviewed, and 👍 reactions on their issues and PRs.

"First authored" is the user's oldest issue or PR. Comments aren't included because GitHub can't cheaply say when someone first commented.

**Pull requests** lists the 5 most recent merged PRs with their rework, and up to 5 open PRs.

## Rework

Rework is how much of a merged PR someone else changed:

```
rework = lines other people added or deleted / lines the author added
credit = 1 - min(1, rework)
```

A 1,000-line PR where a maintainer deleted 900 lines and added 100 has rework 100% and credit 0. A CHANGELOG touch-up barely moves it. This works for squash merges too, because GitHub keeps the PR's commit list.

- Merge commits (such as merging `main` into the branch) are skipped.
- Commits with no linked GitHub account count as the author's.
- If none of the commits are linked to the PR author, the first commit's authors are treated as the contributor (usually the same person on a second account). The report shows "(commits by ...)" when that happens.

## Flags

Flags appear under the header line when something deserves a closer look. They state facts and don't change the score.

| Flag | When |
|---|---|
| Recent PRs mostly not landing | The Pattern penalty is −10 or worse and outweighs everything else in the score, and fewer than half of at least 5 recently resolved PRs merged |
| New account | Under 90 days old, with 20+ PRs across 10+ repos in the last 30 days, even if none have been closed yet |
| Drive-by | PRs in this repo, none merged, and no issues, comments, or reviews here |

```console
hamid-digitalmarketer in YakDriver/oschmod  ·  Novice  ·  averyindex -26
  ⚠ Recent PRs mostly not landing: 3 of 26 resolved merged in 30 days
  ⚠ New account: 17 days old, 109 PRs across 23 repos in 30 days, 3 merged so far
  ⚠ Drive-by: PRs here, none merged, and no issues, comments, or reviews
```

A new account can be legitimate, for example someone doing Hacktoberfest. The flag includes how many PRs merged so a maintainer can judge. `--json` lists flags in a `flags` array with a stable `code` for each.

## Elsewhere on GitHub

The report also shows the user's PR activity across GitHub: account age, PRs opened and merged, merge rate, and PRs opened in the last 30 days with how many repos they went to. That's what separates a real newcomer from an account spraying PRs across dozens of repos.

These numbers only cover public repositories the user doesn't own:

- **Own repos are excluded** because anyone can open and merge PRs in their own repo. Counting them would let an account inflate its merge rate and its "merged elsewhere" bonus for free.
- **Own projects still count, through what other people do with them.** Stars on the user's public, non-fork repos (repos with at least 3 stars) and the number of distinct people whose PRs the user merged into them both add to the score. A maintainer gets credit for projects people use and contribute to. A throwaway account merging its own PRs gets nothing.
- **Private repos are excluded** so everyone running the tool gets the same answer, and private repo names never appear in the report.

## Score

The score has three groups. Each count contributes `weight × log2(1 + count)`, so early contributions matter most and very large counts don't drown out everything else.

**Here** is work in this repository, and it's most of the score.

| Component | Weight |
|---|---|
| Code credit: merged PRs × average credit of the recent ones | 12 |
| Upvotes received | 5 |
| PRs reviewed | 5 |
| Threads commented | 2 |
| Issues opened | 2 |
| PRs closed unmerged | -3 |

Signals that depend on someone else's judgment (a maintainer merged it without rewriting it, other users upvoted it) weigh more than signals a user produces alone, because they're harder to inflate.

**Elsewhere** gives an established developer some benefit of the doubt in a repo they're new to. It's capped at +20 so it can't substitute for work here.

| Component | Weight |
|---|---|
| Account age, in months | 1.5 |
| PRs merged in other public repositories the user doesn't own | 4 |
| Stars on the user's own public repos (repos with 3+ stars) | 2 |
| Distinct people whose PRs the user merged into their own repos (bots excluded) | 4 |

**Pattern** penalizes opening many PRs that don't land. It's uncapped:

```
-6 × log2(1 + PRs opened in the last 30 days) × (1 − landing rate)²
```

The landing rate combines long-term and recent behavior:

- **History sets the expectation.** The user's merge rate on PRs opened before the last 30 days, smoothed so that no history means 50%.
- **Recent resolved PRs update it.** PRs opened in the last 30 days that were merged or closed. History counts as at most 20 PRs' worth of evidence, so a long record cushions a bad month but can't hide dozens of recent PRs that didn't land.
- **Open PRs count as neither.** Some repos take months to review, so a PR waiting for review isn't held against its author.

| Case | Pattern |
|---|---|
| 108 PRs in 17 days from a new account, 3 of 26 resolved merged | −29.8 |
| 83 PRs in 30 days, most still waiting for review, 33 of 33 merged before | −1.5 |
| Long-time contributor with nothing in the last 30 days | 0 |

Because open PRs are neutral, an account whose spray PRs are all still open isn't penalized much until they start getting closed. The new-account flag below covers that case.

| Tier | Score |
|---|---|
| Novice | below 15 |
| Contributor | 15 |
| Regular | 40 |
| Veteran | 80 |
| Core | 150 |

The weights and tiers in [`score.go`](score.go) are a first guess.

## Costs

One user takes about 12 search API calls and 2 or more GraphQL queries, usually 10 to 15 seconds.

GitHub limits search to 30 requests per minute and also has an unpublished secondary limit that trips when you score several users in a row. When either one hits, the tool prints a line to stderr saying how long it's waiting and until when, then retries (up to 3 times, waiting at most 2 minutes each). If the wait would be longer, it says so and exits.

## Development

```sh
make help     # list targets
make ci       # run every check CI runs: build, gofmt, go fix, vet, test
make fix fmt  # apply go fix and gofmt
make install  # install your local build as the gh extension
```

CI runs each check as a separate job on every pull request and on pushes to `main`. `fix-check` fails if `go fix` would change anything, so run `make fix` before pushing.
