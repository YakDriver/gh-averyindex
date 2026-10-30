package main

import (
	"fmt"
	"runtime/debug"
	"time"
)

// version can be set at build time with -ldflags "-X main.version=v1.2.3".
// Otherwise it comes from the build info Go embeds: release builds from a
// tagged checkout report the tag.
var version = ""

// schemaVersion is the --json format version. Increase it when fields are
// removed or change meaning.
//
//	2: threads excludes PRs the user reviewed; thumbs_up excludes the user's
//	   own reactions.
//	3: the new-account-burst flag is gone, and heavy-maintainer-involvement
//	   is new. Code credit leaves out heavy_prs. The "PRs merged elsewhere"
//	   component is weighted by the share accepted by others. New fields:
//	   heavy_prs, heavy_unchecked, and spray_repos, spray_sampled,
//	   accepted_elsewhere, and accepted_sampled in global. recent_repos now
//	   comes from the newest 100 recent PRs, without paging for more.
const schemaVersion = 3

// buildInfo is the version and, when known, the commit and its date.
type buildInfo struct {
	Version string
	Commit  string
	Date    time.Time
}

func currentBuild() buildInfo {
	b := buildInfo{Version: version}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		if b.Version == "" {
			b.Version = "dev"
		}
		return b
	}
	if b.Version == "" {
		b.Version = info.Main.Version
	}
	if b.Version == "" || b.Version == "(devel)" {
		b.Version = "dev"
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Commit = s.Value[:min(7, len(s.Value))]
		case "vcs.time":
			b.Date, _ = time.Parse(time.RFC3339, s.Value)
		}
	}
	return b
}

func (b buildInfo) String() string {
	s := "gh-averyindex " + b.Version
	switch {
	case b.Commit != "" && !b.Date.IsZero():
		s += fmt.Sprintf(" (%s, %s)", b.Commit, b.Date.Format(time.DateOnly))
	case b.Commit != "":
		s += " (" + b.Commit + ")"
	}
	return s
}

// meta describes how a report was produced, so a score can be checked or
// compared later.
type meta struct {
	Version     string
	GeneratedAt time.Time

	// Color highlights the score in the text report. It's only set for a
	// terminal with color enabled, so logs and pipes get plain text.
	Color bool
}
