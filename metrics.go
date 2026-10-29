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
)

// Global is the user's PR activity across all of GitHub, not just this repo.
type Global struct {
	AccountCreated time.Time `json:"account_created,omitzero"`
	AccountAgeDays int       `json:"account_age_days"`

	PRsOpened int `json:"prs_opened"`
	PRsMerged int `json:"prs_merged"`
	PRsOpen   int `json:"prs_open"`

	// RecentPRs is PRs opened anywhere in the last recentDays days.
	RecentPRs int `json:"recent_prs"`
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

// SmoothedMergeRate is MergeRate with one merged and one unmerged PR added,
// so someone with no history starts at 50% instead of 0% or 100%.
func (g Global) SmoothedMergeRate() float64 {
	resolved := g.PRsMerged + g.ClosedUnmerged()
	return float64(g.PRsMerged+1) / float64(resolved+2)
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
	// commented on. Counting threads instead of comments means fifty "+1"s
	// on one issue count once.
	Threads int `json:"threads"`

	// Reviews is the number of other people's PRs the user reviewed.
	Reviews int `json:"reviews"`

	// ThumbsUp is 👍 reactions received on the user's issues and PRs.
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

// MergedElsewhere is PRs merged in repositories other than this one.
func (m Metrics) MergedElsewhere() int {
	return max(0, m.Global.PRsMerged-m.PRsMerged)
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

// DriveBy reports whether the user has opened PRs and done nothing else.
func (m Metrics) DriveBy() bool {
	return m.PRsOpened > 0 && m.Participation() == 0
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

func collectGlobal(src source, acct account, now time.Time, g *Global) error {
	if !acct.CreatedAt.IsZero() {
		g.AccountCreated = acct.CreatedAt
		g.AccountAgeDays = int(now.Sub(acct.CreatedAt).Hours() / 24)
	}

	prs := "is:pr author:" + acct.Login
	for _, c := range []struct {
		dst   *int
		query string
	}{
		{&g.PRsOpened, prs},
		{&g.PRsMerged, prs + " is:merged"},
		{&g.PRsOpen, prs + " is:open"},
	} {
		n, _, err := src.Search(c.query, "", 1)
		if err != nil {
			return err
		}
		*c.dst = n
	}

	since := now.AddDate(0, 0, -recentDays).Format(time.DateOnly)
	total, items, err := src.Search(prs+" created:>="+since, "desc", recentSample)
	if err != nil {
		return err
	}
	g.RecentPRs = total
	g.RecentSampled = len(items)

	perRepo := map[string]int{}
	for _, it := range items {
		perRepo[it.Repo]++
	}
	g.RecentRepos = len(perRepo)
	for r, n := range perRepo {
		// Ties go to the alphabetically first repo so output is stable.
		if n > g.RecentTopCount || (n == g.RecentTopCount && r < g.RecentTopRepo) {
			g.RecentTopRepo, g.RecentTopCount = r, n
		}
	}
	return nil
}

func collect(src source, repo string, acct account, now time.Time) (Metrics, error) {
	user := acct.Login
	m := Metrics{User: user, Repo: repo}
	if err := collectGlobal(src, acct, now, &m.Global); err != nil {
		return m, err
	}

	base := "repo:" + repo
	prs := fmt.Sprintf("%s is:pr author:%s", base, user)
	authored := fmt.Sprintf("%s author:%s", base, user)

	count := func(dst *int, query string) error {
		n, _, err := src.Search(query, "", 1)
		*dst = n
		return err
	}

	for _, c := range []struct {
		dst   *int
		query string
	}{
		{&m.PRsOpened, prs},
		{&m.Issues, fmt.Sprintf("%s is:issue author:%s", base, user)},
		{&m.Threads, fmt.Sprintf("%s commenter:%s -author:%s", base, user, user)},
		{&m.Reviews, fmt.Sprintf("%s is:pr reviewed-by:%s -author:%s", base, user, user)},
	} {
		if err := count(c.dst, c.query); err != nil {
			return m, err
		}
	}

	for _, f := range []struct {
		dst   **searchItem
		order string
	}{
		{&m.FirstAuthored, "asc"},
		{&m.LastAuthored, "desc"},
	} {
		_, items, err := src.Search(authored, f.order, 1)
		if err != nil {
			return m, err
		}
		if len(items) > 0 {
			*f.dst = &items[0]
		}
	}

	var err error
	if m.ThumbsUp, m.ThumbsUpTruncated, err = src.ThumbsUp(authored); err != nil {
		return m, err
	}

	if m.PRsOpened == 0 {
		return m, nil
	}

	if m.PRsOpen, m.OpenPRs, err = src.Search(prs+" is:open", "desc", openPRList); err != nil {
		return m, err
	}

	total, raw, err := src.MergedPRs(prs+" is:merged sort:created-desc", recentMergedSample)
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
