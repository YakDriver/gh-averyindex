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
	repo string
	json bool
	user string
}

const usage = `Usage: gh averyindex [-R owner/repo] [--json] <user>

Summarize how helpful a GitHub user has been in a repository.

Flags:
  -R, --repo owner/repo   Repository to score against (default: current repo)
      --json              Print JSON instead of a table
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

	fullName := repo.Owner + "/" + repo.Name
	m, err := collect(gh, fullName, acct, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	result := score(m, defaultWeights)
	if opts.json {
		err = renderJSON(stdout, m, result)
	} else {
		err = renderText(stdout, m, result)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
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
