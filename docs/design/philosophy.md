# Philosophy

averyindex makes an expensive human judgment cheaper. It doesn't prove that anyone deserves trust.

## Two decisions

averyindex feeds a vouch system, and the vouch system is the gate. The tool supports two decisions with different costs.

| | Seeding and auto-vouch | Vouch on request |
|---|---|---|
| Who decides | The tool, or a bulk list | A maintainer reading the report |
| Cost of a wrong yes | A sprayer gets in without anyone noticing | A few minutes of review |
| Cost of a wrong no | A real contributor waits and asks | A real contributor is turned away |
| What it relies on | Positive signals that other people control | The score, its three parts, and the reason |
| Priority | Hard to inflate | Fast, honest, and slow to condemn |

Penalties barely matter for auto-vouch; a sprayer never gets near the bar. On request, the costs matter more, because a maintainer reads the one line about them. A maintainer may run it hundreds of times a day, so the default output is two lines and the rest is behind `--explain`.

## Two kinds of spray

| | Schemes | Reputation seekers |
|---|---|---|
| Who | Farms promoting a product, a WhatsApp scam, or another scheme | A person padding a résumé or profile with AI at hand |
| How | Automated PRs, often from many accounts, with links or payloads | Many plausible-looking PRs, some of which land |
| Intent | Use the repo | Get credit from the repo |
| Response | Denounce. No conversation. | Decline the vouch, say why, and leave the door open |

Both cost review time, but schemes are much worse. averyindex reports the same facts for both (principle 4), and a maintainer chooses the response. Reputation seekers can earn trust later with work that lands. Schemes don't get that path. Telling the two apart is a maintainer's call, not a score.

## Principles

1. **Design for the threat we have.** Today's PR spraying is cheap and indiscriminate. Write down elaborate gaming, such as sock puppets, staged adoption, or padded reviews, and limit the damage it can do. Don't design around it until the data shows someone doing it.
2. **Engagement is the price of review.** Every PR asks maintainers for review time. Merged PRs and engagement (issues, comments on other people's threads, reviews) earn it. What costs maintainers is the pattern of an AI PR factory: many PRs, fast, with few merged and little else. It's measured as a ratio and a rate. Having no engagement isn't a cost by itself, and neither is authoring occasionally without engaging. Someone who reviews as much as they author pays nothing.
3. **Credit what others validated.** Merges, approvals, and replies from other people count. Activity someone can produce alone counts for little or nothing.
4. **Judge the contribution, not the person.** The question is whether someone's history suggests they help more than they cost maintainers. Whether they're human or use AI is beside the point. A slop label belongs to a PR, not to a person.
5. **Make costly patterns loud, in the score.** Rework, spraying, and authoring without engagement are what cost maintainers. Each is a graded part of the score, and the summary names the biggest one. If a fact matters to the decision, it moves the score. If it doesn't, it belongs in `--explain`. There are no flags or tiers to learn, so adding a new cost doesn't make the output longer.
6. **Evidence, not verdicts.** The score is a summary, its three parts show where it came from, and the reason says why. The maintainer decides. A low score can just mean little visible history.
7. **Cheap and bounded.** Use signals GitHub already exposes. Respect rate limits, keep runs fast, and say when data is incomplete.
8. **Layered defenses.** Vouching controls who can open PRs, and denouncing stops repeat offenders. averyindex only has to describe a first encounter well. Malicious PRs that attack CI are a security problem for other tools.
9. **Change with evidence.** Add defenses when observed patterns justify their cost. State the sample size behind every threshold.

## Why now

- The problem is measurable. A study of 294 repositories and over 2 million PRs and issues found PR volume rose in 2025 while merge rates fell. One-time contributors' merge rates dropped 18% ([arXiv 2607.04003](https://arxiv.org/abs/2607.04003)).
- It's widespread. In a Linux Foundation survey, maintainers estimated that 25% of incoming PRs are AI slop, and 31% in large projects. These are estimates, not measurements ([LF, 2026-10-20](https://www.linuxfoundation.org/press/new-lf-research-survey-shows-generative-ai-cuts-open-source-coding-time-in-half-but-25-of-incoming-pull-requests-are-considered-ai-slop)).
- Platforms are responding. GitHub described the problem in February 2026 ([blog](https://github.blog/open-source/maintainers/welcome-to-the-eternal-september-of-open-source-heres-what-we-plan-to-do-for-maintainers/)), then added settings to restrict PRs and limit how many outside PRs can be open at once ([PR limits](https://github.blog/open-source/maintainers/how-pull-request-limits-are-cutting-down-the-noise/)).
- Some spraying is an attack. The prt-scan campaign used six accounts to open over 500 malicious PRs against `pull_request_target` workflows ([Wiz](https://wiz.io/blog/six-accounts-one-actor-inside-the-prt-scan-supply-chain-campaign)). That's the far end of schemes, and it's out of scope here (principle 8).

Expect sprayers to avoid gated repos before they try to fool a scorer. If vouching becomes common, reputation farming becomes worth their effort. Then revisit principle 1.
