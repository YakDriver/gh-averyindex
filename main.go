// Command gh-averyindex is a gh extension that summarizes how helpful a user
// has been in a repository: merged pull requests, issues, reviews, comment
// threads, and 👍 reactions received, combined into a single averyindex score.
//
// Usage:
//
//	gh averyindex [-R owner/repo] [--json] <user>
//
// Without -R, the repository is resolved from the current directory the same
// way gh does it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/repository"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type options struct {
	repo    string
	json    bool
	version bool
	user    string
}

const usage = `Usage: gh averyindex [-R owner/repo] [--json] <user>
       gh averyindex --version

Summarize how helpful a GitHub user has been in a repository.

Flags:
  -R, --repo owner/repo   Repository to score against (default: current repo)
      --json              Print JSON instead of a table
      --version           Print the version and exit
`

// parseArgs parses flags and the single positional user argument. Flags may
// appear before or after the user.
func parseArgs(args []string, stderr io.Writer) (options, error) {
	var opts options

	fs := flag.NewFlagSet("averyindex", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	fs.StringVar(&opts.repo, "R", "", "")
	fs.StringVar(&opts.repo, "repo", "", "")
	fs.BoolVar(&opts.json, "json", false, "")
	fs.BoolVar(&opts.version, "version", false, "")

	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return opts, err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}

	if opts.version && len(positional) == 0 {
		return opts, nil
	}
	if len(positional) != 1 {
		fs.Usage()
		return opts, errors.New("expected exactly one user")
	}
	opts.user = strings.TrimPrefix(positional[0], "@")
	if opts.user == "" {
		return opts, errors.New("user must not be empty")
	}
	return opts, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if opts.version {
		fmt.Fprintln(stdout, currentBuild())
		return 0
	}

	repo, err := resolveRepo(opts.repo)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	gh, err := newGitHub(repo.Host)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	acct, err := gh.Account(opts.user)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if msg := notAPerson(acct); msg != "" {
		fmt.Fprintln(stderr, msg)
		return 1
	}

	fullName := repo.Owner + "/" + repo.Name
	now := time.Now()
	m, err := collect(gh, fullName, acct, now)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	result := score(m, defaultWeights)
	md := meta{Version: currentBuild().Version, GeneratedAt: now.UTC()}
	if opts.json {
		err = renderJSON(stdout, m, result, md)
	} else {
		err = renderText(stdout, m, result, md)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// notAPerson explains why acct can't be scored, or returns "" if it can.
// averyindex is for deciding whether to vouch for a person, and search
// handles bots and organizations differently, so their numbers would mislead.
func notAPerson(acct account) string {
	switch acct.Type {
	case "Bot":
		return fmt.Sprintf("%s is a bot account. averyindex scores people, so there's nothing to report.", acct.Login)
	case "Organization":
		return fmt.Sprintf("%s is an organization. averyindex scores people, so there's nothing to report.", acct.Login)
	}
	return ""
}

func resolveRepo(flagValue string) (repository.Repository, error) {
	if flagValue != "" {
		r, err := repository.Parse(flagValue)
		if err != nil {
			return r, fmt.Errorf("invalid repository %q: %w", flagValue, err)
		}
		return r, nil
	}
	r, err := repository.Current()
	if err != nil {
		return r, fmt.Errorf("could not determine repository (run inside a clone or pass -R owner/repo): %w", err)
	}
	return r, nil
}
