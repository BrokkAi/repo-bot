package repobot

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/BrokkAi/repo-bot/internal/worker"
)

// Request is what Town asks for: one complete observation of the repository,
// and the branch-health duty that goes with it.
type Request struct {
	// SinceHead is the branch head Town last observed. The inventory names the
	// commits the branch gained beyond it.
	SinceHead string
	// Commits are revisions Town still needs release ancestry for.
	Commits []string
}

// Run observes the repository and, when the branch it covers is failing its
// checks, repairs it. The inventory is reported even when the repair does not
// land: Town's view of the repository must not depend on an agent.
func Run(ctx context.Context, cfg Config, request Request, agent Agent, log *slog.Logger) (worker.Result, error) {
	if err := cfg.Validate(); err != nil {
		return worker.Result{}, err
	}
	cfg, err := cfg.resolved()
	if err != nil {
		return worker.Result{}, err
	}
	if log == nil {
		log = slog.Default()
	}
	report := observe(ctx)
	gh := githubClient{config: cfg}
	report(Progress{Phase: "inventorying", Task: "Reading " + cfg.GitHubRepo()})
	inventory, err := gh.snapshot(ctx)
	if err != nil {
		return worker.Result{}, fmt.Errorf("read repository inventory: %w", err)
	}
	if SHA(request.SinceHead) && request.SinceHead != inventory.Head {
		report(Progress{Phase: "inventorying", Task: "Naming changes since " + short(request.SinceHead)})
		if inventory.Commits, err = gh.changes(ctx, request.SinceHead, inventory.Head); err != nil {
			return worker.Result{}, fmt.Errorf("compare branch changes: %w", err)
		}
	}
	if candidates := ancestryCandidates(inventory, request.Commits); len(candidates) > 0 {
		report(Progress{Phase: "inventorying", Task: "Settling release ancestry"})
		if inventory.Released, err = released(ctx, cfg, gh, inventory, candidates); err != nil {
			return worker.Result{}, err
		}
	}
	report(Progress{Phase: "checking", Task: "Reading checks on " + cfg.Branch})
	health, err := assess(ctx, cfg, gh, agent, inventory.Head, log)
	if err != nil {
		// The inventory is true whatever the health duty did, so it is reported
		// rather than lost with the error.
		return worker.Result{Inventory: &inventory}, err
	}
	return worker.Result{Inventory: &inventory, Health: health}, nil
}

// ancestryCandidates is every revision whose release state this run can settle:
// the ones Town asked about, the commits the branch just gained, and the merge
// commits of pull requests merged into it. Proving one it did not ask for costs
// nothing Town has to interpret.
func ancestryCandidates(inventory Inventory, asked []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(revision string) {
		if SHA(revision) && !seen[revision] {
			seen[revision] = true
			out = append(out, revision)
		}
	}
	for _, revision := range asked {
		add(revision)
	}
	for _, c := range inventory.Commits {
		add(c.SHA)
	}
	for _, p := range inventory.Pulls {
		if p.MergedAt != nil && p.Base.Ref == inventory.Branch {
			add(p.MergeCommit)
		}
	}
	return out
}

// released proves which of the asked-for revisions a published release already
// contains. One comparison names everything the branch carries beyond the
// latest release, which settles most of them without a request of their own;
// absence from that list proves nothing on its own, so anything not named there
// is still proven individually.
func released(ctx context.Context, cfg Config, gh githubClient, inventory Inventory, candidates []string) (map[string]bool, error) {
	latest := latestRelease(inventory.Releases)
	if latest == nil {
		return nil, nil
	}
	saved, err := ReadState(cfg)
	if err != nil {
		return nil, err
	}
	pending := map[string]bool{}
	if saved == nil || saved.VainCompare != latest.Tag {
		unreleased, usable, err := gh.unreleased(ctx, latest.Tag, inventory.Head)
		switch {
		case err != nil:
			// The comparison is an optimization with its own way of saying it
			// proved nothing, so a failure falls back to the individual proofs
			// rather than taking the whole inventory down with it.
		case usable:
			for _, c := range unreleased {
				if SHA(c.SHA) {
					pending[c.SHA] = true
				}
			}
		default:
			// A repository that cuts releases from another branch answers
			// "diverged" forever; paying for that on every poll adds traffic to
			// the one case the comparison cannot help with.
			if err := updateState(cfg, func(s *State) { s.VainCompare = latest.Tag }); err != nil {
				return nil, err
			}
		}
	}
	out := map[string]bool{}
	for _, revision := range candidates {
		if _, done := out[revision]; done || !SHA(revision) {
			continue
		}
		if pending[revision] {
			out[revision] = false
			continue
		}
		included, err := gh.contains(ctx, revision, latest.Tag)
		if err != nil {
			return nil, fmt.Errorf("prove release ancestry of %s: %w", short(revision), err)
		}
		out[revision] = included
	}
	return out, nil
}
