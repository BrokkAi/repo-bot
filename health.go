package repobot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/BrokkAi/repo-bot/internal/osrun"
	"github.com/BrokkAi/repo-bot/internal/worker"
)

type BranchHealth = worker.BranchHealth

// checkReader is the branch verdict this duty works from. The GitHub client
// supplies it in production; tests supply a verdict of their own.
type checkReader interface {
	checks(context.Context, string) (Checks, error)
}

// Branch health states. "repaired" means this run published a commit; the next
// inventory is what proves the branch actually recovered.
const (
	healthGreen        = "green"
	healthPending      = "pending"
	healthUnreported   = "unreported"
	healthRed          = "red"
	healthRepaired     = "repaired"
	healthUnrepairable = "unrepairable"
)

// assess reports the branch's health and repairs a failing branch when this
// workspace still has attempts left for that exact revision. Reading the
// verdict is the run's work and its failure is the run's failure; a repair that
// does not land is reported as health, because the inventory is still true.
func assess(ctx context.Context, cfg Config, verdict checkReader, agent Agent, head string, log *slog.Logger) (*BranchHealth, error) {
	cfg, err := cfg.resolved()
	if err != nil {
		return nil, err
	}
	checks, err := verdict.checks(ctx, head)
	if err != nil {
		return nil, fmt.Errorf("read branch checks: %w", err)
	}
	health := &BranchHealth{State: checks.State, Head: head}
	for _, c := range checks.Failing {
		health.Failing = append(health.Failing, c.Name)
	}
	saved, err := ReadState(cfg)
	if err != nil {
		return nil, err
	}
	if checks.State != healthRed {
		// The branch is not failing, so nothing is owed on this revision and an
		// older revision's spent budget is no longer anyone's business.
		if saved != nil && saved.Head != "" {
			if err := writeState(cfg, &State{}); err != nil {
				return nil, err
			}
		}
		return health, nil
	}
	if cfg.InventoryOnly() {
		health.Detail = "No agent is configured for this house, so the failing branch is reported only."
		return health, nil
	}
	attempts := 0
	if saved != nil && saved.Head == head {
		attempts = saved.Attempts
	}
	health.Attempts = attempts
	if attempts >= cfg.MaxRepairs {
		health.State = healthUnrepairable
		health.Detail = fmt.Sprintf("Spent %d of %d repair attempts on %s without a healthy branch.", attempts, cfg.MaxRepairs, short(head))
		if saved != nil && saved.Failure != "" {
			health.Detail += " Last attempt: " + saved.Failure
		}
		return health, nil
	}
	attempts++
	health.Attempts = attempts
	// The budget is spent before the agent starts. An attempt that crashes the
	// process still cost the repository an agent run, and must not be free.
	if err := writeState(cfg, &State{Head: head, Attempts: attempts}); err != nil {
		return nil, err
	}
	pushed, failure := repair(ctx, cfg, checks, head, agent, log)
	if failure != nil {
		health.Detail = truncate(failure.Error(), 2000)
		if err := writeState(cfg, &State{Head: head, Attempts: attempts, Failure: health.Detail}); err != nil {
			return nil, err
		}
		if rejectedByProtection(failure) {
			// No further attempt on this revision can land, whatever the agent
			// writes, so the budget is not spent one attempt at a time.
			health.State = healthUnrepairable
		}
		return health, nil
	}
	if pushed == "" {
		health.Detail = "The agent left the branch unchanged; nothing was published."
		if err := writeState(cfg, &State{Head: head, Attempts: attempts, Failure: health.Detail}); err != nil {
			return nil, err
		}
		return health, nil
	}
	health.State = healthRepaired
	health.Pushed = pushed
	if err := writeState(cfg, &State{Head: head, Attempts: attempts, Pushed: pushed}); err != nil {
		return nil, err
	}
	return health, nil
}

// repair runs one agent attempt against the exact failing revision in a private
// worktree and publishes the result only when the operator's verification
// passes. It reports the published commit, or an empty name when the agent
// changed nothing.
func repair(ctx context.Context, cfg Config, checks Checks, head string, agent Agent, log *slog.Logger) (string, error) {
	report := observe(ctx)
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(cfg.Timeout))
		defer cancel()
	}
	report(Progress{Phase: "repairing", Task: "Preparing a checkout at " + short(head)})
	clone := checkout{config: cfg}
	if err := clone.open(ctx); err != nil {
		return "", err
	}
	work, err := clone.repairWorktree(ctx, head)
	if err != nil {
		return "", err
	}
	if agent == nil {
		agent = agentProcess{config: work.config, log: log}
	}
	report(Progress{Phase: "repairing", Task: "Repairing " + strings.Join(names(checks.Failing), ", ")})
	if _, err := agent.Execute(ctx, repairPrompt(cfg, cfg.Branch, head, checks.Failing, instructions(cfg, work.config.Directory))); err != nil {
		return "", fmt.Errorf("repair agent: %w", err)
	}
	if len(cfg.Verify) > 0 {
		report(Progress{Phase: "verifying", Task: strings.Join(cfg.Verify, " ")})
		if out, err := osrun.Run(ctx, work.config.Directory, nil, cfg.Verify...); err != nil {
			return "", fmt.Errorf("repair failed verification: %w: %s", err, truncate(out, 2000))
		}
	}
	message := fmt.Sprintf("Repair failing checks on %s\n\nFailing at %s: %s.\n", cfg.Branch, short(head), strings.Join(names(checks.Failing), ", "))
	revision, err := work.commit(ctx, head, message)
	if err != nil || revision == "" {
		return "", err
	}
	if cfg.DryRun {
		return "", errors.New("dry run: the repair was committed locally and not published")
	}
	report(Progress{Phase: "publishing", Task: "Pushing " + short(revision) + " to " + cfg.Branch})
	if err := work.publish(ctx, revision); err != nil {
		return "", fmt.Errorf("publish repair: %w", err)
	}
	return revision, nil
}

func names(checks []Check) []string {
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		out = append(out, c.Name)
	}
	return out
}

func short(revision string) string {
	if len(revision) > 8 {
		return revision[:8]
	}
	return revision
}
