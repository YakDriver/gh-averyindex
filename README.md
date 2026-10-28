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

**Participation** is everything besides the user's own PRs: issues opened, other people's threads they commented on (threads, not comments, so fifty "+1"s on one issue count once), PRs they reviewed, and 👍 reactions on their issues and PRs. If someone has opened PRs and done nothing else, the report says **Drive-by**.

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

## Score

Each count contributes `weight × log2(1 + count)`, so early contributions matter most and very large counts don't drown out everything else.

| Component | Weight |
|---|---|
| Code credit: merged PRs × average credit of the recent ones | 12 |
| Upvotes received | 5 |
| PRs reviewed | 5 |
| Threads commented | 2 |
| Issues opened | 2 |
| PRs closed unmerged | -3 |

Signals that depend on someone else's judgment (a maintainer merged it without rewriting it, other users upvoted it) weigh more than signals a user produces alone, because they're harder to inflate.

| Tier | Score |
|---|---|
| Novice | below 15 |
| Contributor | 15 |
| Regular | 40 |
| Veteran | 80 |
| Core | 150 |

The weights and tiers in [`score.go`](score.go) are a first guess.

## Costs

One user takes about 8 search API calls and 2 or more GraphQL queries, about 15 seconds. The search API allows 30 requests per minute.
