package repobot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/BrokkAi/repo-bot/internal/osrun"
	"github.com/BrokkAi/repo-bot/internal/worker"
)

// Inventory payload types are the protocol's own, so one observation is read,
// validated and reported without a private copy in between.
type (
	Inventory = worker.Inventory
	Issue     = worker.Issue
	Pull      = worker.Pull
	Release   = worker.Release
	Commit    = worker.Commit
)

var sha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// SHA reports a complete, lowercase Git object name. Abbreviations are never
// accepted: ancestry and inventory claims are made about exact revisions.
func SHA(value string) bool { return sha.MatchString(value) }

type githubClient struct{ config Config }

func (g githubClient) api(ctx context.Context, method, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	args := []string{"gh", "api", "--hostname", g.config.GitHub.Host, "--method", method, path}
	text, err := osrun.Run(ctx, "", nil, args...)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(text), out)
}

func (g githubClient) path(suffix string) string { return "repos/" + g.config.GitHubRepo() + suffix }

func pages[T any](ctx context.Context, g githubClient, path string) ([]T, error) {
	all := []T{}
	join := "?"
	if strings.Contains(path, "?") {
		join = "&"
	}
	for page := 1; page <= 1000; page++ {
		var batch []T
		if err := g.api(ctx, "GET", fmt.Sprintf("%s%sper_page=100&page=%d", path, join, page), &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
	return nil, errors.New("GitHub pagination limit reached; refusing an incomplete inventory")
}

// snapshot reads the whole repository: the branch this town covers, its head,
// every issue, pull request and release. Partial reads are errors, never an
// inventory that silently omits what Town would then treat as gone.
func (g githubClient) snapshot(ctx context.Context) (Inventory, error) {
	var out Inventory
	var metadata struct {
		Branch string `json:"default_branch"`
	}
	if err := g.api(ctx, "GET", "repos/"+g.config.GitHubRepo(), &metadata); err != nil {
		return out, err
	}
	out.DefaultBranch = metadata.Branch
	out.Branch = g.config.Branch
	if out.Branch == "" {
		out.Branch = metadata.Branch
	}
	var branch struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := g.api(ctx, "GET", g.path("/branches/"+url.PathEscape(out.Branch)), &branch); err != nil {
		return out, err
	}
	out.Head = branch.Commit.SHA
	if !SHA(out.Head) {
		return out, errors.New("GitHub reported no usable branch head")
	}
	var err error
	if out.Issues, err = pages[Issue](ctx, g, g.path("/issues?state=all&sort=updated&direction=desc")); err != nil {
		return out, err
	}
	if out.Pulls, err = pages[Pull](ctx, g, g.path("/pulls?state=all&sort=updated&direction=desc")); err != nil {
		return out, err
	}
	out.Releases, err = pages[Release](ctx, g, g.path("/releases"))
	return out, err
}

// changes names the commits a branch gained between two exact revisions. A
// comparison that is not a fast-forward proves nothing and names none.
func (g githubClient) changes(ctx context.Context, from, to string) ([]Commit, error) {
	if !SHA(from) || !SHA(to) {
		return nil, errors.New("invalid comparison revisions")
	}
	commits, usable, err := g.compare(ctx, from, to)
	if err != nil || !usable {
		return nil, err
	}
	return commits, nil
}

// unreleased lists the commits a head carries beyond a published tag. The
// second result reports whether the comparison was usable at all: a head that
// has diverged from the tag proves nothing, and the caller must fall back to
// proving each commit on its own rather than assume anything.
func (g githubClient) unreleased(ctx context.Context, tag, head string) ([]Commit, bool, error) {
	if tag == "" || strings.Contains(tag, "..") || !SHA(head) {
		return nil, false, errors.New("invalid release comparison")
	}
	return g.compare(ctx, tag, head)
}

func (g githubClient) compare(ctx context.Context, base, head string) ([]Commit, bool, error) {
	var commits []Commit
	for page := 1; page <= 1000; page++ {
		var result struct {
			Status  string   `json:"status"`
			Commits []Commit `json:"commits"`
		}
		path := fmt.Sprintf("%s?per_page=100&page=%d", g.path("/compare/"+url.PathEscape(base)+"..."+url.PathEscape(head)), page)
		if err := g.api(ctx, "GET", path, &result); err != nil {
			return nil, false, err
		}
		if result.Status != "ahead" && result.Status != "identical" {
			return nil, false, nil
		}
		commits = append(commits, result.Commits...)
		if len(result.Commits) < 100 {
			return commits, true, nil
		}
	}
	return nil, false, errors.New("comparison exceeded pagination limit")
}

// contains proves the commit is an ancestor of the published tag. Publication
// timestamps do not prove which commits a release actually contains.
func (g githubClient) contains(ctx context.Context, revision, tag string) (bool, error) {
	if !SHA(revision) || tag == "" || strings.Contains(tag, "..") {
		return false, errors.New("invalid release ancestry query")
	}
	var result struct {
		Status string `json:"status"`
	}
	err := g.api(ctx, "GET", g.path("/compare/"+revision+"..."+url.PathEscape(tag)), &result)
	return result.Status == "ahead" || result.Status == "identical", err
}

// Check is one reported check or commit status on a revision.
type Check struct {
	Name       string
	Conclusion string
	Summary    string
	URL        string
}

// Checks is the whole reported verdict for one revision.
type Checks struct {
	// State is "green" when every reported check concluded acceptably,
	// "pending" while any is still running, "red" when one failed, and
	// "unreported" when the revision has no checks at all.
	State   string
	Failing []Check
}

func failingConclusion(value string) bool {
	switch value {
	case "failure", "timed_out", "action_required", "startup_failure", "stale":
		return true
	}
	return false
}

// checks reads both check runs and commit statuses for a revision. A revision
// nothing reports on is "unreported", which is never treated as healthy work
// done: it is simply not a failure to repair.
func (g githubClient) checks(ctx context.Context, revision string) (Checks, error) {
	var out Checks
	if !SHA(revision) {
		return out, errors.New("invalid revision for a check query")
	}
	runs, err := checkRuns(ctx, g, revision)
	if err != nil {
		return out, err
	}
	reported, pending := 0, false
	for _, r := range runs {
		reported++
		if r.Status != "completed" {
			pending = true
			continue
		}
		if failingConclusion(r.Conclusion) {
			out.Failing = append(out.Failing, Check{Name: r.Name, Conclusion: r.Conclusion, Summary: summarize(r.Output.Title, r.Output.Summary), URL: r.DetailsURL})
		}
	}
	var combined struct {
		State    string `json:"state"`
		Statuses []struct {
			Context     string `json:"context"`
			State       string `json:"state"`
			Description string `json:"description"`
			URL         string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := g.api(ctx, "GET", g.path("/commits/"+revision+"/status"), &combined); err != nil {
		return out, err
	}
	for _, s := range combined.Statuses {
		reported++
		switch s.State {
		case "failure", "error":
			out.Failing = append(out.Failing, Check{Name: s.Context, Conclusion: s.State, Summary: s.Description, URL: s.URL})
		case "pending":
			pending = true
		}
	}
	switch {
	case len(out.Failing) > 0:
		out.State = "red"
	case pending:
		out.State = "pending"
	case reported == 0:
		out.State = "unreported"
	default:
		out.State = "green"
	}
	return out, nil
}

type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"details_url"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output"`
}

func checkRuns(ctx context.Context, g githubClient, revision string) ([]checkRun, error) {
	var all []checkRun
	for page := 1; page <= 100; page++ {
		var result struct {
			Total int        `json:"total_count"`
			Runs  []checkRun `json:"check_runs"`
		}
		path := fmt.Sprintf("%s?per_page=100&page=%d", g.path("/commits/"+revision+"/check-runs"), page)
		if err := g.api(ctx, "GET", path, &result); err != nil {
			return nil, err
		}
		all = append(all, result.Runs...)
		if len(result.Runs) < 100 {
			return all, nil
		}
	}
	return nil, errors.New("check runs exceeded pagination limit")
}

func summarize(title, summary string) string {
	text := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(title), strings.TrimSpace(summary)}, "\n"))
	return truncate(text, 2000)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n… truncated"
}

func latestRelease(releases []Release) *Release {
	var latest *Release
	for i := range releases {
		r := &releases[i]
		if !r.Draft && !r.Prerelease && (latest == nil || r.At.After(latest.At)) {
			latest = r
		}
	}
	return latest
}
