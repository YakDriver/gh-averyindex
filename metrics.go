package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
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

	// Spray looks at the newest spraySample PRs opened from sprayDays ago
	// until the recent window starts, so it never counts the PRs that
	// not-landing already does.
	sprayDays   = 180
	spraySample = 100
	// acceptedSample is how many PRs merged elsewhere are checked for who
	// accepted them.
	acceptedSample = 100

	// A merged PR here had heavy maintainer involvement when it drew
	// heavyMinThreads review threads and others made heavyMinCommits
	// commits on it. Discussion alone doesn't count: an author who works
	// through a long review makes the commits themselves.
	heavyMinThreads = 20
	heavyMinCommits = 10
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

	// SprayRepos is how many repos, among the newest SpraySampled PRs opened
	// 31 to 180 days ago, got a PR that was closed unmerged and none that
	// merged. PRs still open don't count.
	SprayRepos   int `json:"spray_repos"`
	SpraySampled int `json:"spray_sampled"`

	// AcceptedElsewhere is how many of the newest AcceptedSampled PRs merged
	// elsewhere someone else merged or approved. Merging your own PR
	// without anyone's approval shows nothing about what others think of it.
	AcceptedElsewhere int `json:"accepted_elsewhere"`
	AcceptedSampled   int `json:"accepted_sampled"`
}

// AcceptedShare is the fraction of sampled PRs merged elsewhere that someone
// else merged or approved, or 1 when none were sampled.
func (g Global) AcceptedShare() float64 {
	if g.AcceptedSampled == 0 {
		return 1
	}
	return float64(g.AcceptedElsewhere) / float64(g.AcceptedSampled)
}

// AcceptedMergedElsewhere is PRs merged elsewhere, weighted by the share
// that others accepted.
func (g Global) AcceptedMergedElsewhere() float64 {
	return float64(g.MergedElsewhere) * g.AcceptedShare()
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

// HeavyPR is a merged PR that drew a long review and many commits from
// others. It earns no code credit.
type HeavyPR struct {
	Number            int `json:"number"`
	ReviewThreads     int `json:"review_threads"`
	MaintainerCommits int `json:"maintainer_commits"`
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
	HeavyPRs     []HeavyPR    `json:"heavy_prs,omitempty"`
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
// rework, leaving out heavy PRs, which earn nothing on their own. It's 1 if
// none are left.
func (m Metrics) AvgCredit() float64 {
	var sum float64
	var n int
	for _, pr := range m.RecentMerged {
		if !m.heavy(pr.Number) {
			sum += pr.Credit
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return sum / float64(n)
}

func (m Metrics) heavy(number int) bool {
	return slices.ContainsFunc(m.HeavyPRs, func(h HeavyPR) bool { return h.Number == number })
}

// CodeCredit is merged PRs other than heavy ones, discounted by the average
// rework on recent ones.
func (m Metrics) CodeCredit() float64 {
	return float64(max(0, m.PRsMerged-len(m.HeavyPRs))) * m.AvgCredit()
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
	commits := nonMerge(pr.Commits)

	owners, inferred := prOwners(user, commits)

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

// nonMerge drops merge commits, which carry the base branch's changes.
func nonMerge(commits []rawCommit) []rawCommit {
	var out []rawCommit
	for _, c := range commits {
		if !c.Merge {
			out = append(out, c)
		}
	}
	return out
}

// prOwners is who counts as the PR's contributor: the user, or, if no commit
// is linked to them, the first commit's authors (returned as inferred).
func prOwners(user string, commits []rawCommit) (owners, inferred []string) {
	owners = []string{user}
	if !slices.ContainsFunc(commits, func(c rawCommit) bool { return ownedBy(owners, c) }) && len(commits) > 0 {
		owners = commits[0].Logins
		inferred = owners
	}
	return owners, inferred
}

// maintainerCommits counts non-merge commits on a PR made by someone other
// than its contributor. Bot commits, such as generated changelog entries,
// don't count.
func maintainerCommits(user string, pr rawPR) int {
	commits := nonMerge(pr.Commits)
	owners, _ := prOwners(user, commits)
	n := 0
	for _, c := range commits {
		if !ownedBy(owners, c) && !slices.ContainsFunc(c.Logins, isBotLogin) {
			n++
		}
	}
	return n
}

func isBotLogin(login string) bool {
	return strings.HasSuffix(login, "[bot]")
}

// sprayRepos counts repos where a sampled PR was closed unmerged and none
// merged.
func sprayRepos(items []searchItem) int {
	merged, closed := map[string]bool{}, map[string]bool{}
	for _, it := range items {
		switch it.State {
		case "MERGED":
			merged[it.Repo] = true
		case "CLOSED":
			closed[it.Repo] = true
		}
	}
	n := 0
	for r := range closed {
		if !merged[r] {
			n++
		}
	}
	return n
}

// acceptedByOthers counts merged PRs that someone besides user merged or
// approved.
func acceptedByOthers(user string, items []searchItem) int {
	n := 0
	for _, it := range items {
		if !strings.EqualFold(it.MergedBy, user) || it.Approvals > 0 {
			n++
		}
	}
	return n
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

// result adds a search whose whole result goes in dst.
func (b *batch) result(dst *searchResult, query, order string, n int) {
	b.reqs = append(b.reqs, searchReq{Query: query, Order: order, N: n})
	b.sets = append(b.sets, func(r searchResult) { *dst = r })
}

// extra adds a newest-first search that returns more about each PR.
func (b *batch) extra(dst *searchResult, e extra, query string, n int) {
	b.reqs = append(b.reqs, searchReq{Query: query, Order: "desc", N: n, Extra: e})
	b.sets = append(b.sets, func(r searchResult) { *dst = r })
}

// split makes a batch of each search.
func (b *batch) split() []batch {
	var out []batch
	for i := range b.reqs {
		out = append(out, batch{reqs: b.reqs[i : i+1], sets: b.sets[i : i+1]})
	}
	return out
}

// runAll sends the batches at once and returns the first error.
func runAll(src source, batches []batch) error {
	errs := make([]error, len(batches))
	var wg sync.WaitGroup
	for i := range batches {
		wg.Go(func() { errs[i] = batches[i].run(src) })
	}
	wg.Wait()
	return cmp.Or(errs...)
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

	// The searches with extras are sent separately, alongside the main
	// batch. Together, GitHub can time out or exceed its resource limits on
	// a prolific user.
	var bx batch
	sprayFrom := now.AddDate(0, 0, -sprayDays).Format(time.DateOnly)
	sprayTo := now.AddDate(0, 0, -recentDays-1).Format(time.DateOnly)
	var spray searchResult
	bx.extra(&spray, extraState, global+" created:"+sprayFrom+".."+sprayTo, spraySample)
	var accepted searchResult
	bx.extra(&accepted, extraAccepted, global+" is:merged -repo:"+repo, acceptedSample)

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

	if err := runAll(src, append([]batch{b}, bx.split()...)); err != nil {
		return m, err
	}
	if len(first) > 0 {
		m.FirstAuthored = &first[0]
	}
	if len(last) > 0 {
		m.LastAuthored = &last[0]
	}

	g.RecentPRs = recentFirst.Total
	countRecentRepos(recentFirst.Items, g)
	g.SpraySampled, g.SprayRepos = len(spray.Items), sprayRepos(spray.Items)
	g.AcceptedSampled, g.AcceptedElsewhere = len(accepted.Items), acceptedByOthers(user, accepted.Items)
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
	if total == 0 {
		return m, nil
	}
	reviewed, err := src.ReviewedPRs(repo, prs+" is:merged", heavyMinThreads)
	if err != nil {
		return m, err
	}
	for _, pr := range reviewed {
		if n := maintainerCommits(user, pr); n >= heavyMinCommits {
			m.HeavyPRs = append(m.HeavyPRs, HeavyPR{Number: pr.Number, ReviewThreads: pr.ReviewThreads, MaintainerCommits: n})
		}
	}
	return m, nil
}

// countRecentRepos counts the repos the user's newest recent PRs went to.
func countRecentRepos(items []searchItem, g *Global) {
	perRepo := map[string]int{}
	for _, it := range items {
		perRepo[it.Repo]++
	}
	g.RecentSampled = len(items)
	g.RecentRepos = len(perRepo)
	for r, n := range perRepo {
		// Ties go to the alphabetically first repo so output is stable.
		if n > g.RecentTopCount || (n == g.RecentTopCount && r < g.RecentTopRepo) {
			g.RecentTopRepo, g.RecentTopCount = r, n
		}
	}
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
