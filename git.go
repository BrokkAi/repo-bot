package repobot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BrokkAi/repo-bot/internal/osrun"
)

type checkout struct{ config Config }

func (g checkout) git(ctx context.Context, args ...string) (string, error) {
	return osrun.Run(ctx, g.config.Directory, map[string]string{"GIT_TERMINAL_PROMPT": "0"}, append([]string{"git"}, args...)...)
}

// open makes the managed clone usable and current. The clone belongs to this
// bot alone: a directory that is not its own root, or points elsewhere, is
// refused rather than adopted.
func (g checkout) open(ctx context.Context) error {
	if _, err := os.Stat(g.config.Directory); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(g.config.Directory), 0700); err != nil {
			return err
		}
		if _, err := osrun.Run(ctx, "", map[string]string{"GIT_TERMINAL_PROMPT": "0"}, "git", "clone", "--branch", g.config.Branch, "--", g.config.Remote, g.config.Directory); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	root, err := g.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if root != g.config.Directory {
		return errors.New("directory must be the root of a managed clone")
	}
	remote, err := g.git(ctx, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if remote != g.config.Remote {
		return errors.New("managed clone origin differs from configuration")
	}
	if _, err := g.git(ctx, "check-ref-format", "refs/heads/"+g.config.Branch); err != nil {
		return err
	}
	_, err = g.git(ctx, "fetch", "--prune", "origin", "+refs/heads/"+g.config.Branch+":refs/remotes/origin/"+g.config.Branch)
	return err
}

// repairWorktree gives the agent a private checkout at the exact failing
// revision, never the shared clone. A worktree left by an earlier attempt is
// reset to that revision so one attempt never inherits another's edits.
func (g checkout) repairWorktree(ctx context.Context, revision string) (checkout, error) {
	if !SHA(revision) {
		return checkout{}, errors.New("invalid repair revision")
	}
	dir := filepath.Join(g.config.Directory+"-repairs", "branch")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
			return checkout{}, err
		}
		if _, err := g.git(ctx, "worktree", "add", "--detach", "--", dir, revision); err != nil {
			return checkout{}, err
		}
	} else if err != nil {
		return checkout{}, err
	}
	w := g
	w.config.Directory = dir
	if _, err := w.git(ctx, "reset", "--hard", revision); err != nil {
		return checkout{}, err
	}
	if _, err := w.git(ctx, "clean", "-fdx"); err != nil {
		return checkout{}, err
	}
	head, err := w.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return checkout{}, err
	}
	if head != revision {
		return checkout{}, fmt.Errorf("repair worktree is at %s, not the failing revision %s", head, revision)
	}
	return w, nil
}

// commit records the agent's repair on top of base. It reports an empty commit
// name when the agent changed nothing, which is not an error: there is simply
// nothing to publish.
func (g checkout) commit(ctx context.Context, base, message string) (string, error) {
	if _, err := g.git(ctx, "add", "--all"); err != nil {
		return "", err
	}
	if _, err := g.git(ctx, "diff", "--cached", "--quiet"); err == nil {
		return "", nil
	}
	if _, err := g.git(ctx, "-c", "user.name=repo-bot", "-c", "user.email=repo-bot@users.noreply.github.com", "commit", "--no-verify", "--message", message); err != nil {
		return "", err
	}
	head, err := g.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	parent, err := g.git(ctx, "rev-parse", "HEAD^")
	if err != nil {
		return "", err
	}
	if parent != base {
		return "", fmt.Errorf("repair commit sits on %s, not the failing revision %s", parent, base)
	}
	return head, nil
}

// publish fast-forwards the branch to the repair. Git refuses anything else, so
// a branch that moved while the agent worked is left alone and observed again.
func (g checkout) publish(ctx context.Context, revision string) error {
	if !SHA(revision) {
		return errors.New("invalid revision to publish")
	}
	_, err := g.git(ctx, "push", "origin", revision+":refs/heads/"+g.config.Branch)
	return err
}

// rejectedByProtection reports a push refused by the repository's own rules
// rather than by staleness, which no further attempt on this revision can fix.
func rejectedByProtection(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"protected branch", "branch protection", "required status check", "pull request is required", "gh006"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
