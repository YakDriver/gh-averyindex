# gh-averyindex

A [gh](https://cli.github.com/) extension that shows how helpful a user has been in a repository. It's meant for maintainers deciding whether to vouch for someone.

```console
$ gh averyindex YakDriver
YakDriver in hashicorp/terraform-provider-aws  ·  ➜ averyindex 266
  Here +246  ·  Elsewhere +20  ·  Burden +0  ·  1,686 PRs merged here
```

The score is the sum of three parts. **Here** is what the user has given this repo. **Elsewhere** is their record in other repos. **Burden** is what they cost maintainers here, mainly authoring many PRs without engaging. The phrase at the end names the biggest factor. When a cost is big next to what the user has earned, it names the cost. Here's a user who opened 41 PRs in 36 days, with 1 small merge and no other engagement:

```console
someone in hashicorp/terraform-provider-aws  ·  ➜ averyindex -6
  Here +20  ·  Elsewhere -1  ·  Burden -25  ·  41 PRs here, 1 merged, no issues, comments, or reviews
```

`--explain` prints everything behind the score: participation, recent merged PRs with rework, activity elsewhere, and every line of the breakdown.

## Why trust it

AI tools made it cheap to produce plausible pull requests, comments, and issues. They didn't make it cheap to get a maintainer to merge your code unchanged, or to get other people to upvote your bug report or contribute to your project. averyindex weighs those signals most.

- **It counts other people's judgments.** Merged PRs, rework, upvotes, and contributors to a user's projects all depend on someone else. Things a person produces alone (comments, issues) count for less, and PRs they merge into their own repositories don't count.
- **It shows its work.** Every point in the score is on a line in `--explain`, with the count behind it. If you disagree with the number, you can see exactly why it came out that way.
- **Anyone gets the same answer.** Activity outside the scored repo comes only from public repositories, and each version scores the same way for everyone. Every report records the version and time it was generated, so an old score can be checked.
- **It reports. It doesn't decide.** The score is a quick read for a maintainer, not a gate. There's no threshold that admits or rejects anyone, and no tiers or labels to interpret.
- **It says what it can't see.** See [Limits](#limits).

## Install

```sh
gh extension install YakDriver/gh-averyindex
```

## Usage

```sh
gh averyindex [-R owner/repo] [--explain | --json] <user>
gh averyindex [-R owner/repo] [--explain | --json] --pr <number>
gh averyindex [--explain | --json] <pull request or issue URL>
gh averyindex --version
```

Pass a username, or score the author of a pull request or issue with `--pr` or by pasting its URL. `--pr` takes issue numbers too. A URL sets the repository, so it works from anywhere.

Otherwise, run it inside a clone to score against that repository, or pass `-R`. It uses your existing `gh` login, so it works on any repository you can read.

`--json` output includes `schema_version`, `version`, and `generated_at` alongside the metrics, the score and its three parts, the reason, and every component. `schema_version` changes when a field is removed or changes meaning.

## What goes into it

**Here** is what the user has given this repo: PRs merged, weighed by **rework** (how much someone else changed before merging), 👍 reactions other people gave the user's issues and PRs, PRs reviewed, other people's threads commented on (threads, not comments, so fifty "+1"s count once), and issues opened.

**Elsewhere** gives an established developer some benefit of the doubt in a repo they're new to. It counts account age, PRs merged in other people's public repos (weighted by the share someone else merged or approved), and what other people do with the user's own projects (stars and outside contributors). That part is capped at 20, so it can't stand in for work here. Elsewhere leaves out the scored repo. It also counts what the user's record elsewhere costs, with no cap:

- **Not landing:** the last 30 days of PRs weighed against the user's longer history, so a long record cushions a bad month.
- **Spray:** repos where a PR from the 150 days before that was closed unmerged and none merged.
- **Authoring without engagement elsewhere:** the same measure as in Burden, over the last 30 days in other repos.

**Burden** is what the user costs maintainers here:

- **Heavy maintainer involvement:** a merged PR with 20 or more review threads and 10 or more commits by others earns no credit. A long review the author worked through alone doesn't count.
- **Authoring without engagement:** the pattern of an AI PR factory, meaning many PRs, fast, with few merged and little else. For each PR, the user should have about one merged PR, or one issue, comment, or review of someone else's work. The cost grows with how far short they fall and how many PRs they opened, past the first 3. It's full at 10 PRs a month and smaller below that, so an occasional contributor who only authors pays little. Someone who reviews and discusses as much as they author pays nothing, however many PRs were closed.

Signals that depend on someone else's judgment count more than things a person produces alone, because they're harder to inflate. Exact weights are in the code. The reasoning is in [docs/design/philosophy.md](docs/design/philosophy.md) and [docs/design/scoring-v2.md](docs/design/scoring-v2.md).

## Limits

- **Only GitHub.** Work on other forges, mailing lists, or inside companies isn't visible.
- **Private repositories are excluded** from everything outside the scored repo.
- **Organization repos count as elsewhere.** Excluding a user's own repos doesn't cover repos owned by an organization they control. Self-merged PRs without approval count for less, which covers most of that.
- **Search returns at most 1,000 results.** Upvotes received can be partial, and the report says so when they are.
- **Samples of 100.** Repo counts come from the newest 100 PRs in each window, and acceptance from the newest 100 PRs merged elsewhere. Heavy maintainer involvement is checked on the 100 most recently updated merged PRs here, reading the last 100 commits of the 10 most discussed. The spray count can be off in either direction when a repo's merged or closed PRs fall outside the sample.
- **A brand-new account with only open PRs** gets no spray line, since spray starts at day 31. The "Last 30 days" line shows its age, PR count, and repos.
- **Rework is sampled** from the 5 most recently merged PRs (picked from the 25 most recently updated, since search can't sort by merge date), and from the first 100 commits of each. When none of a PR's commits are linked to its author, the first commit's authors are assumed to be the contributor, and the report says "(commits by ...)".
- **Self-upvotes are checked among the 10 oldest 👍 on each issue or PR.** Someone who upvotes their own issue usually does it first.
- **"First authored" ignores comments.** It's the user's oldest issue or PR in the repo.
- **Contributors to own projects** count distinct non-bot accounts, so a user's own automation account registered as a regular user counts once.
- **Results change over time** as PRs merge and close. Check the timestamp at the end of the report.
- **Bots and organizations aren't scored.** The tool says so and exits.

## Costs

A user's searches go to GitHub as one GraphQL request. Two searches that need more detail per PR go out as separate requests at the same time, because combining them with the rest can exceed GitHub's limits for a prolific user. A few more requests cover reactions, rework, long reviews, and the user's own repos. A run usually takes 6 to 15 seconds, and longer for someone with thousands of issues and PRs in the repo. It costs a few GraphQL points out of 5,000 per hour. Searches all go through GraphQL rather than the REST search API and its limit of 30 requests per minute, so scoring several people in a row normally doesn't stall. In a terminal, a spinner shows while it works, and the score is bold. Neither appears when output is piped or redirected, though the `➜` pointing at the score always does. `NO_COLOR` turns off bold, and `TERM=dumb` switches the spinner to ASCII.

If GitHub rate-limits the tool or returns a temporary server error (502, 503, or 504), it says on stderr how long it's waiting and retries.

## Development

```sh
make help     # list targets
make ci       # run every check CI runs: build, gofmt, go fix, vet, test
make fix fmt  # apply go fix and gofmt
make install  # install your local build as the gh extension
```

CI runs each check as a separate job on every pull request and on pushes to `main`. `fix-check` fails if `go fix` would change anything, so run `make fix` before pushing.
