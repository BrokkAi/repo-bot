package repobot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const stateFormat = 1

// State is what this bot remembers between runs: which failing revision it has
// already spent attempts on. It exists so a poll every minute cannot start an
// agent every minute on the same red branch.
type State struct {
	Format   int    `json:"format"`
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Head     string `json:"head,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
	Pushed   string `json:"pushed,omitempty"`
	Failure  string `json:"failure,omitempty"`
	// VainCompare is a release tag whose comparison with the branch head proved
	// nothing. A repository that cuts releases from another branch answers that
	// way forever, so it is not asked again for the same tag.
	VainCompare string    `json:"vain_compare,omitempty"`
	Updated     time.Time `json:"updated,omitempty"`
}

func statePath(cfg Config) string { return filepath.Join(cfg.StateDirectory, "repo-bot.json") }

// ReadState returns the durable repair state, or nil when this workspace has
// none yet. A state written for another repository or branch is not this
// workspace's memory and is reported as an error rather than silently reused.
func ReadState(cfg Config) (*State, error) {
	f, err := os.Open(statePath(cfg))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s State
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("read repair state: %w", err)
	}
	if s.Format != stateFormat {
		return nil, fmt.Errorf("unsupported repair state format %d", s.Format)
	}
	if s.Repo != cfg.GitHubRepo() || s.Branch != cfg.Branch {
		return nil, fmt.Errorf("repair state belongs to %s@%s", s.Repo, s.Branch)
	}
	return &s, nil
}

// updateState reads, changes and saves this workspace's memory in one step, so
// one duty's record never erases another's.
func updateState(cfg Config, change func(*State)) error {
	saved, err := ReadState(cfg)
	if err != nil {
		return err
	}
	if saved == nil {
		saved = &State{}
	}
	change(saved)
	return writeState(cfg, saved)
}

func writeState(cfg Config, s *State) error {
	s.Format = stateFormat
	s.Repo = cfg.GitHubRepo()
	s.Branch = cfg.Branch
	s.Updated = time.Now().UTC()
	if err := os.MkdirAll(cfg.StateDirectory, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(cfg.StateDirectory, "repo-bot-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s); err != nil {
		f.Close()
		os.Remove(name)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, statePath(cfg)); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
