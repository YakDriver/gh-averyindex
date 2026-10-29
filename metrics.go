package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// recentMergedSample is how many recent merged PRs are checked for rework.
	recentMergedSample = 5
	// openPRList is how many open PRs are listed.
	openPRList = 5
	// recentDays is the window for recent PR activity across GitHub.
	recentDays = 30
	// recentSample is how many recent PRs are examined to count repos.
	recentSample = 100
	// minProjectStars is the fewest stars a user's own repo needs before its
	// stars count. It filters out a self-star plus a friend on every repo.
	minProjectStars = 3
	// contributorSample is how many merged PRs in the user's own repos are
	// examined to count distinct contributors.
	contributorSample = 100
)

// Global is the user's PR activity across GitHub. It covers public
// repositories the user doesn't own: anyone can open and merge PRs in their
// own repos, and private repos would make the result depend on who runs the
// tool.
type Global struct {
	AccountCreated time.Time `json:"account_created,omitzero"`
	AccountAgeDays int       `json:"account_age_days"`

	PRsOpened int `json:"prs_opened"`
	PRsMerged int `json:"prs_merged"`
	PRsOpen   int `json:"prs_open"`

	// MergedElsewhere is PRs merged in those repositories, excluding the one
	// being scored.
	MergedElsewhere int `json:"merged_elsewhere"`

	// Projects maintained: what other people did with the user's own public
	// repositories. Self-merged PRs in those repos aren't counted because
	// nobody else judged them, but other people starring and contributing to
	// the projects is.
	ProjectStars       int  `json:"project_stars"`
	ProjectsStarred    int  `json:"projects_starred"`
	Contributors       int  `json:"contributors"`
	ContributorsSample bool `json:"contributors_sampled,omitempty"`

	// RecentPRs is PRs opened anywhere in the last recentDays days, and
	// RecentMerged and RecentClosedUnmerged are how those PRs resolved so far.
	// PRs still open count as neither, however long they've waited.
	RecentPRs            int `json:"recent_prs"`
	RecentMerged         int `json:"recent_merged"`
	RecentClosedUnmerged int `json:"recent_closed_unmerged"`
	// RecentRepos and RecentTopRepo come from the newest RecentSampled of
	// those PRs.
	RecentRepos    int    `json:"recent_repos"`
	RecentTopRepo  string `json:"recent_top_repo,omitempty"`
	RecentTopCount int    `json:"recent_top_count,omitempty"`
	RecentSampled  int    `json:"recent_sampled"`
}

// ClosedUnmerged is PRs closed without merging, anywhere.
func (g Global) ClosedUnmerged() int {
	return max(0, g.PRsOpened-g.PRsMerged-g.PRsOpen)
}

// MergeRate is merged PRs as a fraction of PRs no longer open, anywhere.
// It returns false if no PRs have been resolved.
func (g Global) MergeRate() (float64, bool) {
	resolved := g.PRsMerged + g.ClosedUnmerged()
	if resolved == 0 {
		return 0, false
	}
	return float64(g.PRsMerged) / float64(resolved), true
}

// priorWeight caps how many PRs' worth of evidence the user's history before
// the recent window counts for. A long record cushions a bad month but
// can't outweigh dozens of recent PRs that didn't land.
const priorWeight = 20

// HistoryMerged and HistoryResolved are merged and resolved PRs opened
// before the recent window.
func (g Global) HistoryMerged() int {
	return max(0, g.PRsMerged-g.RecentMerged)
}

func (g Global) HistoryResolved() int {
	return g.HistoryMerged() + max(0, g.ClosedUnmerged()-g.RecentClosedUnmerged)
}

// LandingRate estimates how likely the user's PRs are to be merged. History
// before the recent window sets the expectation, weighted as at most
// priorWeight PRs, and recent resolved PRs update it. With no history the
// expectation is 50%, so a first PR barely registers.
func (g Global) LandingRate() float64 {
	prior := float64(g.HistoryMerged()+1) / float64(g.HistoryResolved()+2)
	strength := float64(min(g.HistoryResolved(), priorWeight) + 2)
	recentResolved := g.RecentMerged + g.RecentClosedUnmerged
	return (float64(g.RecentMerged) + prior*strength) / (float64(recentResolved) + strength)
}

// MergedPR is a recent merged PR with its rework.
type MergedPR struct {
	Number    int       `json:"number"`
	MergedAt  time.Time `json:"merged_at"`
	Additions int       `json:"additions"`
	Deletions int       `json:"deletions"`
	// Rework is lines other people added or deleted on the PR, as a fraction
	// of lines the author added. It can exceed 1.
	Rework float64 `json:"rework"`
	// Credit is 1 - min(1, Rework).
	Credit float64 `json:"credit"`
	// CommitsBy is set when none of the PR's commits were linked to its
	// author, so the first commit's authors were treated as the contributor.
	CommitsBy        []string `json:"commits_by,omitempty"`
	CommitsTruncated bool     `json:"commits_truncated,omitempty"`
}

// Metrics are the raw counts for one user in one repository.
type Metrics struct {
	User string `json:"user"`
	Repo string `json:"repo"`

	PRsOpened int `json:"prs_opened"`
	PRsMerged int `json:"prs_merged"`
	PRsOpen   int `json:"prs_open"`

	Issues int `json:"issues"`

	// Threads is the number of other people's issues and PRs the user
	// commented on, not counting PRs they reviewed. Counting threads instead
	// of comments means fifty "+1"s on one issue count once.
	Threads int `json:"threads"`

	// Reviews is the number of other people's PRs the user reviewed.
	Reviews int `json:"reviews"`

	// ThumbsUp is 👍 reactions other people gave the user's issues and PRs.
	ThumbsUp          int  `json:"thumbs_up"`
	ThumbsUpTruncated bool `json:"thumbs_up_truncated,omitempty"`

	// FirstAuthored and LastAuthored are the user's oldest and newest issue
	// or PR. Nil if they've never opened one.
	FirstAuthored *searchItem `json:"first_authored,omitempty"`
	LastAuthored  *searchItem `json:"last_authored,omitempty"`

	RecentMerged []MergedPR   `json:"recent_merged,omitempty"`
	OpenPRs      []searchItem `json:"open_prs,omitempty"`

	Global Global `json:"global"`
}

// PRsClosedUnmerged is PRs the user opened that were closed without merging.
func (m Metrics) PRsClosedUnmerged() int {
	// Search counts are computed independently and can briefly disagree.
	return max(0, m.PRsOpened-m.PRsMerged-m.PRsOpen)
}

// Participation is everything besides the user's own PRs that can be counted:
// issues opened, threads commented on, and PRs reviewed.
func (m Metrics) Participation() int {
	return m.Issues + m.Threads + m.Reviews
}

// DriveBy reports whether the user has opened PRs here, none were merged, and
// they've done nothing else here. Someone with merged PRs has already been
// through review, so they aren't flagged.
func (m Metrics) DriveBy() bool {
	return m.PRsOpened > 0 && m.PRsMerged == 0 && m.Participation() == 0
}

// AvgCredit is the average credit across the recent merged PRs checked for
// rework, or 1 if none were checked.
func (m Metrics) AvgCredit() float64 {
	if len(m.RecentMerged) == 0 {
		return 1
	}
	var sum float64
	for _, pr := range m.RecentMerged {
		sum += pr.Credit
	}
	return sum / float64(len(m.RecentMerged))
}

// CodeCredit is merged PRs discounted by the average rework on recent ones.
func (m Metrics) CodeCredit() float64 {
	return float64(m.PRsMerged) * m.AvgCredit()
}

// rework compares lines other people changed on a PR with lines its author
// added. Merge commits are skipped because they carry the base branch's
// changes. Commits with no linked GitHub user are given to the author; that's
// usually a contributor committing with an email GitHub doesn't recognize.
//
// If no commit is linked to user, the authors of the first commit are taken
// to be the contributor (often the same person under a second account, such
// as a work email) and returned as inferred.
func rework(user string, pr rawPR) (rework, credit float64, inferred []string) {
	var commits []rawCommit
	for _, c := range pr.Commits {
		if !c.Merge {
			commits = append(commits, c)
		}
	}

	owners := []string{user}
	if !slices.ContainsFunc(commits, func(c rawCommit) bool { return ownedBy(owners, c) }) && len(commits) > 0 {
		owners = commits[0].Logins
		inferred = owners
	}

	var own, ownChurn, other int
	for _, c := range commits {
		if ownedBy(owners, c) {
			own += c.Additions
			ownChurn += c.Additions + c.Deletions
		} else {
			other += c.Additions + c.Deletions
		}
	}

	// A deletion-only PR has no additions, so fall back to all its lines.
	denom := own
	if denom == 0 {
		denom = ownChurn
	}
	if denom == 0 {
		if other == 0 {
			return 0, 1, inferred
		}
		return 1, 0, inferred
	}
	rework = float64(other) / float64(denom)
	return rework, 1 - min(1, rework), inferred
}

func ownedBy(owners []string, c rawCommit) bool {
	if len(c.Logins) == 0 {
		return true
	}
	for _, l := range c.Logins {
		for _, o := range owners {
			if strings.EqualFold(l, o) {
				return true
			}
		}
	}
	return false
}

// globalScope limits a search to public repositories the user doesn't own.
func globalScope(login string) string {
	return "is:public -user:" + login
}

// batch collects searches so they can be sent together, and records where
// each result goes.
type batch struct {
	reqs []searchReq
	sets []func(searchResult)
}

// count adds a search whose total goes in dst.
func (b *batch) count(dst *int, query string) {
	b.reqs = append(b.reqs, searchReq{Query: query})
	b.sets = append(b.sets, func(r searchResult) { *dst = r.Total })
}

// items adds a search whose total and first n items go in total and items.
func (b *batch) items(total *int, items *[]searchItem, query, order string, n int) {
	b.reqs = append(b.reqs, searchReq{Query: query, Order: order, N: n})
	b.sets = append(b.sets, func(r searchResult) {
		if total != nil {
			*total = r.Total
		}
		*items = r.Items
	})
}

// result adds a search whose whole result, including its cursor, goes in dst.
func (b *batch) result(dst *searchResult, query, order string, n int) {
	b.reqs = append(b.reqs, searchReq{Query: query, Order: order, N: n})
	b.sets = append(b.sets, func(r searchResult) { *dst = r })
}

func (b *batch) run(src source) error {
	results, err := src.SearchMany(b.reqs)
	if err != nil {
		return err
	}
	if len(results) != len(b.reqs) {
		return fmt.Errorf("got %d search results for %d searches", len(results), len(b.reqs))
	}
	for i, r := range results {
		b.sets[i](r)
	}
	return nil
}

func collect(src source, repo string, acct account, now time.Time) (Metrics, error) {
	user := acct.Login
	m := Metrics{User: user, Repo: repo}
	g := &m.Global
	if !acct.CreatedAt.IsZero() {
		g.AccountCreated = acct.CreatedAt
		g.AccountAgeDays = int(now.Sub(acct.CreatedAt).Hours() / 24)
	}

	// Every search goes out in one request.
	var b batch

	// Across GitHub: public repos the user doesn't own.
	global := fmt.Sprintf("is:pr author:%s %s", user, globalScope(user))
	b.count(&g.PRsOpened, global)
	b.count(&g.PRsMerged, global+" is:merged")
	b.count(&g.PRsOpen, global+" is:open")
	b.count(&g.MergedElsewhere, global+" is:merged -repo:"+repo)

	since := now.AddDate(0, 0, -recentDays).Format(time.DateOnly)
	recent := global + " created:>=" + since
	var recentFirst searchResult
	b.result(&recentFirst, recent, "desc", recentSample)
	b.count(&g.RecentMerged, recent+" is:merged")
	b.count(&g.RecentClosedUnmerged, recent+" is:closed is:unmerged")

	var contributorTotal int
	var contributorItems []searchItem
	b.items(&contributorTotal, &contributorItems,
		fmt.Sprintf("is:pr is:merged is:public user:%s -author:%s", user, user), "desc", contributorSample)

	// This repo.
	base := "repo:" + repo
	prs := fmt.Sprintf("%s is:pr author:%s", base, user)
	authored := fmt.Sprintf("%s author:%s", base, user)
	b.count(&m.PRsOpened, prs)
	b.count(&m.Issues, fmt.Sprintf("%s is:issue author:%s", base, user))
	// PRs the user reviewed count under Reviews, so they're left out here to
	// avoid counting the same work twice.
	b.count(&m.Threads, fmt.Sprintf("%s commenter:%s -author:%s -reviewed-by:%s", base, user, user, user))
	b.count(&m.Reviews, fmt.Sprintf("%s is:pr reviewed-by:%s -author:%s", base, user, user))
	var first, last []searchItem
	b.items(nil, &first, authored, "asc", 1)
	b.items(nil, &last, authored, "desc", 1)
	b.items(&m.PRsOpen, &m.OpenPRs, prs+" is:open", "desc", openPRList)

	if err := b.run(src); err != nil {
		return m, err
	}
	if len(first) > 0 {
		m.FirstAuthored = &first[0]
	}
	if len(last) > 0 {
		m.LastAuthored = &last[0]
	}

	g.RecentPRs = recentFirst.Total
	if err := countRecentRepos(src, recent, recentFirst, g); err != nil {
		return m, err
	}
	if err := collectProjects(src, user, contributorTotal, contributorItems, g); err != nil {
		return m, err
	}

	var err error
	if m.ThumbsUp, m.ThumbsUpTruncated, err = src.ThumbsUp(authored, user); err != nil {
		return m, err
	}

	if m.PRsOpened == 0 {
		return m, nil
	}
	total, raw, err := src.MergedPRs(repo, prs+" is:merged", recentMergedSample)
	if err != nil {
		return m, err
	}
	m.PRsMerged = total
	for _, pr := range raw {
		rw, credit, inferred := rework(user, pr)
		m.RecentMerged = append(m.RecentMerged, MergedPR{
			Number:           pr.Number,
			MergedAt:         pr.MergedAt,
			Additions:        pr.Additions,
			Deletions:        pr.Deletions,
			Rework:           rw,
			Credit:           credit,
			CommitsBy:        inferred,
			CommitsTruncated: pr.CommitsTruncated,
		})
	}
	return m, nil
}

// countRecentRepos counts the repos the user's recent PRs went to, starting
// from the first page of results.
func countRecentRepos(src source, recent string, page searchResult, g *Global) error {
	items := page.Items
	perRepo := map[string]int{}
	for _, it := range items {
		perRepo[it.Repo]++
	}
	// The first page can miss repos when the newest PRs all went to a few of
	// them. Keep reading until there are enough repos to decide the
	// new-account flag or the results run out. GitHub search stops at 1,000.
	for len(perRepo) < burstMinRepos && page.HasNext && len(items) < searchResultLimit {
		next, err := src.SearchNext(recent, "desc", page.EndCursor)
		if err != nil {
			return err
		}
		if len(next.Items) == 0 {
			break
		}
		page = next
		items = append(items, next.Items...)
		for _, it := range next.Items {
			perRepo[it.Repo]++
		}
	}
	g.RecentSampled = len(items)
	g.RecentRepos = len(perRepo)
	for r, n := range perRepo {
		// Ties go to the alphabetically first repo so output is stable.
		if n > g.RecentTopCount || (n == g.RecentTopCount && r < g.RecentTopRepo) {
			g.RecentTopRepo, g.RecentTopCount = r, n
		}
	}
	return nil
}

// collectProjects scores what other people do with the user's own repos.
func collectProjects(src source, login string, contributorTotal int, contributorItems []searchItem, g *Global) error {
	stars, err := src.OwnedRepoStars(login)
	if err != nil {
		return err
	}
	for _, n := range stars {
		if n >= minProjectStars {
			g.ProjectStars += n
			g.ProjectsStarred++
		}
	}

	people := map[string]bool{}
	for _, it := range contributorItems {
		// A login differing only in case is the same account.
		if it.Author != "" && !it.AuthorBot {
			people[strings.ToLower(it.Author)] = true
		}
	}
	g.Contributors = len(people)
	g.ContributorsSample = contributorTotal > len(contributorItems)
	return nil
}
