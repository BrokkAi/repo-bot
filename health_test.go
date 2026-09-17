package repobot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type verdict struct {
	result Checks
	err    error
	reads  int
}

func (v *verdict) checks(context.Context, string) (Checks, error) {
	v.reads++
	return v.result, v.err
}

type agentFunc func(context.Context, string) (string, error)

func (f agentFunc) Execute(ctx context.Context, prompt string) (string, error) { return f(ctx, prompt) }

const failingHead = "1111111111111111111111111111111111111111"

func TestGreenBranchSpendsNoAttemptAndForgetsAnOlderFailure(t *testing.T) {
	cfg := workspace(t)
	if err := writeState(cfg, &State{Head: failingHead, Attempts: 3, Failure: "gave up"}); err != nil {
		t.Fatal(err)
	}
	called := false
	agent := agentFunc(func(context.Context, string) (string, error) { called = true; return "", nil })
	health, err := assess(context.Background(), cfg, &verdict{result: Checks{State: healthGreen}}, agent, failingHead, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthGreen || called {
		t.Fatalf("a green branch must not start an agent: %+v", health)
	}
	saved, err := ReadState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Head != "" || saved.Attempts != 0 {
		t.Fatalf("a healthy branch must leave no spent budget behind: %+v", saved)
	}
}

func TestPendingChecksAreNotRepaired(t *testing.T) {
	cfg := workspace(t)
	agent := agentFunc(func(context.Context, string) (string, error) {
		t.Fatal("a branch whose checks are still running must not be repaired")
		return "", nil
	})
	health, err := assess(context.Background(), cfg, &verdict{result: Checks{State: healthPending}}, agent, failingHead, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthPending {
		t.Fatalf("unexpected health %+v", health)
	}
}

func TestRepairBudgetIsSpentPerRevision(t *testing.T) {
	cfg := workspace(t)
	origin, head := originRepository(t)
	cfg.Remote = origin
	cfg.Branch = "main"
	cfg.MaxRepairs = 2
	red := &verdict{result: Checks{State: healthRed, Failing: []Check{{Name: "build", Conclusion: "failure"}}}}
	attempts := 0
	agent := agentFunc(func(context.Context, string) (string, error) {
		attempts++
		return "", errors.New("agent gave up")
	})
	for i := 1; i <= 2; i++ {
		health, err := assess(context.Background(), cfg, red, agent, head, nil)
		if err != nil {
			t.Fatal(err)
		}
		if health.State != healthRed || health.Attempts != i {
			t.Fatalf("attempt %d reported %+v", i, health)
		}
	}
	health, err := assess(context.Background(), cfg, red, agent, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthUnrepairable || attempts != 2 {
		t.Fatalf("an exhausted budget must stop the agent: %+v after %d attempts", health, attempts)
	}
	if !strings.Contains(health.Detail, "agent gave up") {
		t.Fatalf("the operator must be told why: %q", health.Detail)
	}
	// A new failing revision is a new problem and starts its own budget.
	other := commitTo(t, origin, "Second commit")
	health, err = assess(context.Background(), cfg, red, agent, other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthRed || health.Attempts != 1 || attempts != 3 {
		t.Fatalf("a new failing revision must get a fresh budget: %+v", health)
	}
}

func TestProtectedBranchEndsTheRepairRatherThanRetrying(t *testing.T) {
	cfg := workspace(t)
	origin, head := originRepository(t)
	cfg.Remote = origin
	cfg.Branch = "main"
	cfg.MaxRepairs = 3
	red := &verdict{result: Checks{State: healthRed, Failing: []Check{{Name: "build", Conclusion: "failure"}}}}
	agent := agentFunc(func(context.Context, string) (string, error) {
		return "", errors.New("publish repair: GH006: Protected branch update failed")
	})
	health, err := assess(context.Background(), cfg, red, agent, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthUnrepairable {
		t.Fatalf("a push the repository itself refuses cannot be retried into success: %+v", health)
	}
}

func TestInventoryOnlyConfigurationReportsWithoutRepairing(t *testing.T) {
	cfg := workspace(t)
	cfg.Agent.Command = nil
	red := &verdict{result: Checks{State: healthRed, Failing: []Check{{Name: "build", Conclusion: "failure"}}}}
	health, err := assess(context.Background(), cfg, red, nil, failingHead, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthRed || health.Failing[0] != "build" {
		t.Fatalf("unexpected health %+v", health)
	}
	if saved, _ := ReadState(cfg); saved != nil {
		t.Fatal("reporting a failure must not spend a repair budget")
	}
}

// TestRepairPublishesAVerifiedFixOntoTheBranch drives the whole duty against a
// real repository: the agent's edit is committed on the exact failing revision
// and fast-forwarded onto the branch only after verification passes.
func TestRepairPublishesAVerifiedFixOntoTheBranch(t *testing.T) {
	origin, head := originRepository(t)
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.Remote = origin
	cfg.Branch = "main"
	cfg.Directory = filepath.Join(root, "checkout")
	cfg.StateDirectory = filepath.Join(root, "state")
	cfg.GitHub.Repo = "acme/orchard"
	cfg.Verify = []string{"test", "-f", "fixed.txt"}
	red := &verdict{result: Checks{State: healthRed, Failing: []Check{{Name: "build", Conclusion: "failure"}}}}
	var seen string
	agent := agentFunc(func(_ context.Context, prompt string) (string, error) {
		seen = prompt
		return "", os.WriteFile(filepath.Join(cfg.Directory+"-repairs", "branch", "fixed.txt"), []byte("repaired\n"), 0600)
	})
	health, err := assess(context.Background(), cfg, red, agent, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthRepaired || !SHA(health.Pushed) {
		t.Fatalf("a verified repair must be published: %+v", health)
	}
	if !strings.Contains(seen, "build") {
		t.Fatal("the agent must be told which check failed")
	}
	if published := gitOutput(t, origin, "rev-parse", "refs/heads/main"); published != health.Pushed {
		t.Fatalf("branch is at %s, expected the repair %s", published, health.Pushed)
	}
	if parent := gitOutput(t, origin, "rev-parse", "refs/heads/main^"); parent != head {
		t.Fatalf("the repair must sit on the failing revision %s, not %s", head, parent)
	}
}

func TestUnverifiedRepairIsNotPublished(t *testing.T) {
	origin, head := originRepository(t)
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.Remote = origin
	cfg.Branch = "main"
	cfg.Directory = filepath.Join(root, "checkout")
	cfg.StateDirectory = filepath.Join(root, "state")
	cfg.GitHub.Repo = "acme/orchard"
	cfg.Verify = []string{"false"}
	red := &verdict{result: Checks{State: healthRed, Failing: []Check{{Name: "build", Conclusion: "failure"}}}}
	agent := agentFunc(func(context.Context, string) (string, error) {
		return "", os.WriteFile(filepath.Join(cfg.Directory+"-repairs", "branch", "fixed.txt"), []byte("repaired\n"), 0600)
	})
	health, err := assess(context.Background(), cfg, red, agent, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthRed || health.Pushed != "" {
		t.Fatalf("a repair that fails verification must stay unpublished: %+v", health)
	}
	if published := gitOutput(t, origin, "rev-parse", "refs/heads/main"); published != head {
		t.Fatalf("branch moved to %s despite failed verification", published)
	}
}

func TestAgentThatChangesNothingPublishesNothing(t *testing.T) {
	origin, head := originRepository(t)
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.Remote = origin
	cfg.Branch = "main"
	cfg.Directory = filepath.Join(root, "checkout")
	cfg.StateDirectory = filepath.Join(root, "state")
	cfg.GitHub.Repo = "acme/orchard"
	red := &verdict{result: Checks{State: healthRed, Failing: []Check{{Name: "build", Conclusion: "failure"}}}}
	agent := agentFunc(func(context.Context, string) (string, error) { return "nothing to do", nil })
	health, err := assess(context.Background(), cfg, red, agent, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != healthRed || health.Pushed != "" {
		t.Fatalf("unexpected health %+v", health)
	}
	if published := gitOutput(t, origin, "rev-parse", "refs/heads/main"); published != head {
		t.Fatalf("branch moved to %s without a repair", published)
	}
}

func originRepository(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "origin")
	git(t, "", "init", "--initial-branch=main", dir)
	git(t, dir, "config", "user.email", "origin@example.test")
	git(t, dir, "config", "user.name", "Origin")
	git(t, dir, "config", "receive.denyCurrentBranch", "ignore")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("orchard\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "--all")
	git(t, dir, "commit", "--message", "First commit")
	return dir, gitOutput(t, dir, "rev-parse", "HEAD")
}

// commitTo adds one commit to the origin's branch and returns its revision.
func commitTo(t *testing.T, origin, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(origin, strings.ReplaceAll(message, " ", "-")+".txt"), []byte(message+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, origin, "add", "--all")
	git(t, origin, "commit", "--message", message)
	return gitOutput(t, origin, "rev-parse", "HEAD")
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitOutput(t, dir, args...)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
