// Package repobot observes one GitHub repository for Brokk Town and keeps the
// branch it covers healthy. It reports a complete inventory and, when the
// branch is failing its checks, repairs it with an agent.
package repobot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BrokkAi/acp-go/runner"
)

type Duration time.Duration

func (d *Duration) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(value)
	if err == nil {
		*d = Duration(parsed)
	}
	return err
}
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

type AgentConfig = runner.AgentConfig

type GitHubConfig struct {
	Repo string `json:"repo,omitempty"`
	Host string `json:"host"`
}

type Config struct {
	Remote           string       `json:"remote"`
	Branch           string       `json:"branch"`
	Directory        string       `json:"directory"`
	StateDirectory   string       `json:"state_directory"`
	InstructionFiles []string     `json:"instruction_files"`
	Agent            AgentConfig  `json:"agent"`
	GitHub           GitHubConfig `json:"github"`
	Timeout          Duration     `json:"timeout"`
	// MaxRepairs bounds the agent attempts spent on one failing revision. The
	// budget is per head: a new failing commit starts a fresh one.
	MaxRepairs int `json:"max_repairs"`
	// Verify is the operator's command. A repair is pushed only when it passes.
	Verify []string `json:"verify,omitempty"`
	// DryRun leaves the repair in the worktree and publishes nothing.
	DryRun bool `json:"dry_run"`
}

func DefaultConfig() Config {
	return Config{
		Branch: "master", Directory: "var/checkout", StateDirectory: "var/state",
		InstructionFiles: []string{"AGENTS.md", "README.md"},
		Agent:            AgentConfig{Command: []string{"codex-acp"}}, GitHub: GitHubConfig{Host: "github.com"},
		Timeout: Duration(time.Hour), MaxRepairs: 3,
	}
}

func ReadConfig(filename string) (Config, error) {
	cfg := DefaultConfig()
	f, err := os.Open(filename)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return cfg, errors.New("expected one configuration object")
	}
	base, err := filepath.Abs(filepath.Dir(filename))
	if err != nil {
		return cfg, err
	}
	if cfg.Remote != "" && !strings.HasPrefix(cfg.Remote, "-") && !isRemoteURL(cfg.Remote) && !filepath.IsAbs(cfg.Remote) {
		cfg.Remote = filepath.Join(base, cfg.Remote)
	}
	for _, path := range []*string{&cfg.Directory, &cfg.StateDirectory} {
		if !filepath.IsAbs(*path) {
			*path = filepath.Join(base, *path)
		}
		if *path, err = canonical(*path); err != nil {
			return cfg, err
		}
	}
	return cfg, cfg.Validate()
}

func isRemoteURL(raw string) bool {
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		return true
	}
	colon := strings.IndexByte(raw, ':')
	slash := strings.IndexByte(raw, '/')
	return colon >= 0 && (slash < 0 || colon < slash) && !filepath.IsAbs(raw)
}

func canonical(path string) (string, error) { return resolvePath(filepath.Clean(path), 0) }

func resolvePath(path string, depth int) (string, error) {
	if depth > 255 {
		return "", errors.New("too many symlink levels")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	parent, err = resolvePath(parent, depth+1)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

var slug = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func (c Config) GitHubRepo() string {
	if c.GitHub.Repo != "" {
		return c.GitHub.Repo
	}
	path := ""
	if prefix := "git@" + c.GitHub.Host + ":"; strings.HasPrefix(c.Remote, prefix) {
		path = strings.TrimPrefix(c.Remote, prefix)
	}
	if u, err := url.Parse(c.Remote); err == nil && u.Hostname() == c.GitHub.Host {
		path = strings.TrimPrefix(u.Path, "/")
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	if slug.MatchString(path) {
		return path
	}
	return ""
}

// resolved canonicalizes the workspace paths. Git reports the real path of a
// checkout, so a directory reached through a symlink must be resolved before it
// is compared with one, or every managed clone looks foreign.
func (c Config) resolved() (Config, error) {
	var err error
	if c.Directory, err = canonical(c.Directory); err != nil {
		return c, err
	}
	c.StateDirectory, err = canonical(c.StateDirectory)
	return c, err
}

// InventoryOnly reports whether this configuration can observe the repository
// but must not attempt a repair. Observation needs no agent and no checkout.
func (c Config) InventoryOnly() bool { return len(c.Agent.Command) == 0 || c.Agent.Command[0] == "" }

func (c Config) Validate() error {
	if c.Remote == "" || strings.HasPrefix(c.Remote, "-") {
		return errors.New("remote is required")
	}
	if c.Branch == "" || strings.HasPrefix(c.Branch, "-") || strings.Contains(c.Branch, "..") {
		return errors.New("invalid branch")
	}
	if c.Directory == "" || c.StateDirectory == "" {
		return errors.New("directory and state_directory are required")
	}
	for _, pair := range [][2]string{{c.Directory, c.StateDirectory}, {c.StateDirectory, c.Directory}, {c.Directory + "-repairs", c.StateDirectory}, {c.StateDirectory, c.Directory + "-repairs"}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return err
		}
		if rel == "." || filepath.IsLocal(rel) {
			return errors.New("checkout and state directories must not overlap")
		}
	}
	if c.Agent.Effort != "" && strings.TrimSpace(c.Agent.Effort) == "" {
		return errors.New("agent.effort requires a reasoning effort value")
	}
	if c.GitHub.Repo != "" && !slug.MatchString(c.GitHub.Repo) {
		return errors.New("github.repo must be owner/repository")
	}
	if c.GitHub.Host == "" || strings.ContainsAny(c.GitHub.Host, " /:\\") {
		return errors.New("invalid github.host")
	}
	if c.GitHubRepo() == "" {
		return errors.New("github.repo is required when the remote does not name one")
	}
	if len(c.Verify) > 0 && c.Verify[0] == "" {
		return errors.New("verify command is empty")
	}
	for _, name := range c.InstructionFiles {
		if !filepath.IsLocal(name) {
			return fmt.Errorf("instruction file must be relative: %s", name)
		}
	}
	if c.Timeout <= 0 || c.MaxRepairs < 1 || c.MaxRepairs > 10 {
		return errors.New("timeout must be positive and max_repairs must be between 1 and 10")
	}
	return nil
}
