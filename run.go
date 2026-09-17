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
	if len(request.Commits) > 0 {
		report(Progress{Phase: "inventorying", Task: "Settling release ancestry"})
		if inventory.Released, err = released(ctx, gh, inventory, request.Commits); err != nil {
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

// released proves which of the asked-for revisions a published release already
// contains. One comparison names everything the branch carries beyond the
// latest release, which settles most of them without a request of their own;
// absence from that list proves nothing on its own, so anything not named there
// is still proven individually.
func released(ctx context.Context, gh githubClient, inventory Inventory, candidates []string) (map[string]bool, error) {
	latest := latestRelease(inventory.Releases)
	if latest == nil {
		return nil, nil
	}
	pending := map[string]bool{}
	if unreleased, usable, err := gh.unreleased(ctx, latest.Tag, inventory.Head); err == nil && usable {
		for _, c := range unreleased {
			if SHA(c.SHA) {
				pending[c.SHA] = true
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
