# gh-averyindex

A [gh](https://cli.github.com/) extension that shows how helpful a user has been in a repository. It's meant for maintainers deciding whether to vouch for someone.

```console
$ gh averyindex johnthedev97
johnthedev97 in hashicorp/terraform-provider-aws  ·  Regular  ·  ➜ averyindex 68

Participation
  Issues opened      0
  Threads commented  6
  PRs reviewed       0
  Upvotes received   49
  ...

Pull requests  3 merged · 1 open · 1 closed unmerged
  #19195  merged 2022-03-15  +153 -103  rework 288%  credit 0.00
  #9372   merged 2020-02-21  +1,975 -0  rework  12%  credit 0.88
  #9261   merged 2019-07-10  +349 -0    rework   0%  credit 1.00  (commits by crjohn)
  #50684  open since 2026-10-27

Elsewhere on GitHub (public repos the user doesn't own)
  ...

Score
  Here
    Code credit (3 merged × 0.63)                 1.9    +18.3
    Upvotes received                               49    +28.2
    ...
➜ averyindex                                  Regular       68

averyindex v0.3.0 · 2026-10-29 17:54 UTC
```

The score is for a quick read, not a verdict. Look at the breakdown.

## Why trust it

AI tools made it cheap to produce plausible pull requests, comments, and issues. They didn't make it cheap to get a maintainer to merge your code unchanged, or to get other people to upvote your bug report or contribute to your project. averyindex weighs those signals most.

- **It counts other people's judgments.** Merged PRs, rework, upvotes, and contributors to a user's projects all depend on someone else. Things a person produces alone (comments, issues) count for less, and PRs they merge into their own repositories don't count.
- **It shows its work.** Every point in the score is on a line in the report, with the count behind it. If you disagree with the number, you can see exactly why it came out that way.
- **Anyone gets the same answer.** Activity outside the scored repo comes only from public repositories, and each version scores the same way for everyone. Every report records the version and time it was generated, so an old score can be checked.
- **It reports. It doesn't decide.** The score is a quick read for a maintainer, not a gate. There's no threshold that admits or rejects anyone. [Flags](#flags) point out patterns worth a closer look and don't change the score.
- **It says what it can't see.** See [Limits](#limits).

## Install

```sh
gh extension install YakDriver/gh-averyindex
```

## Usage

```sh
gh averyindex [-R owner/repo] [--json] <user>
gh averyindex [-R owner/repo] [--json] --pr <number>
gh averyindex [--json] <pull request or issue URL>
gh averyindex --version
```

Pass a username, or score the author of a pull request or issue with `--pr` or by pasting its URL. `--pr` takes issue numbers too. A URL sets the repository, so it works from anywhere.

Otherwise, run it inside a clone to score against that repository, or pass `-R`. It uses your existing `gh` login, so it works on any repository you can read.

`--json` output includes `schema_version`, `version`, and `generated_at` alongside the metrics, score, and flags. `schema_version` changes when a field is removed or changes meaning.

## What goes into it

**Participation** in this repo: issues opened, other people's threads commented on (threads, not comments, so fifty "+1"s on one issue count once), PRs reviewed (not also counted as threads), and 👍 reactions other people gave the user's issues and PRs.

**Pull requests** in this repo, with **rework** for recent merged ones: how much of each PR someone else changed before it merged. A PR a maintainer mostly rewrote earns little credit. A CHANGELOG touch-up barely matters. A merged PR with a long review (20+ review threads) and 10 or more commits by others had **heavy maintainer involvement** and earns no credit. A long review the author worked through alone doesn't count.

**Elsewhere on GitHub**: account age, PRs merged in other people's public repos, and what other people do with the user's own projects (stars and outside contributors). PRs a user merges into their own repos don't count, since nobody else judged them. Elsewhere, merged PRs count by the share someone else merged or approved, so merging your own PRs without review counts for less. This gives an established developer some benefit of the doubt in a repo they're new to, and it's capped so it can't stand in for work here.

**Pattern**: penalties for opening PRs that don't land. One weighs the last 30 days against the user's longer history, so a long record cushions a bad month without hiding a burst of rejected PRs. The other covers the 150 days before that: it counts repos where a PR was closed unmerged and none merged. A few cost almost nothing, 20 cost about 7 points, and it tops out near 15. The two cover different PRs, so none counts twice. PRs still waiting for review don't count against anyone.

Signals that depend on someone else's judgment count more than things a person produces alone, because they're harder to inflate. The score maps to a tier: Novice, Contributor, Regular, Veteran, or Core. Exact weights are in the code.

## Flags

Flags appear under the header line when something deserves a closer look. They state facts and don't change the score.

| Flag | When |
|---|---|
| Recent PRs mostly not landing | The Pattern penalty outweighs everything else, and fewer than half of the user's recently resolved PRs merged |
| Drive-by | PRs in this repo, none merged, and no issues, comments, or reviews here |
| Heavy maintainer involvement | A merged PR here had a long review and many commits by others |

```console
hamid-digitalmarketer in YakDriver/oschmod  ·  Novice  ·  ➜ averyindex -17
  ⚠ Recent PRs mostly not landing: 7 of 34 resolved merged in 30 days
  ⚠ Drive-by: PRs here, none merged, and no issues, comments, or reviews
```

Flags are for yes-or-no facts. Spreading PRs across many repos is a matter of degree, so it's a line in the score instead of a flag. `--json` lists flags in a `flags` array with a stable `code` for each. Why the score works the way it does: [docs/design/philosophy.md](docs/design/philosophy.md) and [docs/design/tuning.md](docs/design/tuning.md).

## Limits

- **Only GitHub.** Work on other forges, mailing lists, or inside companies isn't visible.
- **Private repositories are excluded** from everything outside the scored repo.
- **Organization repos count as elsewhere.** Excluding a user's own repos doesn't cover repos owned by an organization they control. Self-merged PRs without approval count for less, which covers most of that.
- **Search returns at most 1,000 results.** Upvotes received can be partial, and the report says so when they are.
- **Samples of 100.** Repo counts come from the newest 100 PRs in each window, and acceptance from the newest 100 PRs merged elsewhere. Heavy maintainer involvement is checked on the 100 most recently updated merged PRs here, with commits fetched for the 10 most discussed.
- **Rework is sampled** from the 5 most recently merged PRs (picked from the 25 most recently updated, since search can't sort by merge date), and from the first 100 commits of each. When none of a PR's commits are linked to its author, the first commit's authors are assumed to be the contributor, and the report says "(commits by ...)".
- **Self-upvotes are checked among the 10 oldest 👍 on each issue or PR.** Someone who upvotes their own issue usually does it first.
- **"First authored" ignores comments.** It's the user's oldest issue or PR in the repo.
- **Contributors to own projects** count distinct non-bot accounts, so a user's own automation account registered as a regular user counts once.
- **Results change over time** as PRs merge and close. Check the timestamp at the end of the report.
- **Bots and organizations aren't scored.** The tool says so and exits.

## Costs

A user's searches go to GitHub as one GraphQL request, sent alongside two smaller searches that need more detail per PR. Together they can exceed GitHub's limits for a prolific user. A few more requests cover reactions, rework, long reviews, and the user's own repos. A run usually takes 6 to 15 seconds, and longer for someone with thousands of issues and PRs in the repo. It costs a few GraphQL points out of 5,000 per hour. Searches all go through GraphQL rather than the REST search API and its limit of 30 requests per minute, so scoring several people in a row normally doesn't stall. In a terminal, a spinner shows while it works, and the score is bold. Neither appears when output is piped or redirected, though the `➜` pointing at the score always does. `NO_COLOR` turns off bold, and `TERM=dumb` switches the spinner to ASCII.

If GitHub rate-limits the tool or returns a temporary server error (502, 503, or 504), it says on stderr how long it's waiting and retries.

## Development

```sh
make help     # list targets
make ci       # run every check CI runs: build, gofmt, go fix, vet, test
make fix fmt  # apply go fix and gofmt
make install  # install your local build as the gh extension
```

CI runs each check as a separate job on every pull request and on pushes to `main`. `fix-check` fails if `go fix` would change anything, so run `make fix` before pushing.
