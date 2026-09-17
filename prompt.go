package repobot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Version is the release identity reported to agents and to Town. The build
// stamps the binary's own version over it.
var Version = "dev"

// repairPrompt asks for the smallest change that makes the branch's own checks
// pass again. It names the failing checks and what they reported; it never
// suggests disabling, skipping or weakening a check to reach green.
func repairPrompt(cfg Config, branch, revision string, failing []Check, instructions string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are repo-bot, working on the %s repository.\n\n", cfg.GitHubRepo())
	fmt.Fprintf(&b, "The default branch %s is failing its checks at commit %s. Your job is to make the branch healthy again.\n\n", branch, revision)
	b.WriteString("## Failing checks\n\n")
	for _, c := range failing {
		fmt.Fprintf(&b, "### %s (%s)\n", c.Name, c.Conclusion)
		if c.URL != "" {
			fmt.Fprintf(&b, "%s\n", c.URL)
		}
		if strings.TrimSpace(c.Summary) != "" {
			fmt.Fprintf(&b, "\n%s\n", c.Summary)
		}
		b.WriteString("\n")
	}
	b.WriteString("## What is expected of you\n\n")
	b.WriteString("- Reproduce the failure locally in this checkout before changing anything.\n")
	b.WriteString("- Fix the cause. Make the smallest change that makes the check pass for the right reason.\n")
	b.WriteString("- Never disable, skip, weaken, or delete a check, a test, or an assertion to reach green, and never edit CI configuration to stop running one.\n")
	b.WriteString("- Do not commit, push, or touch Git history: the work you leave in this checkout is committed and published for you.\n")
	b.WriteString("- If the branch cannot be repaired here, leave the checkout unchanged and explain what you found.\n")
	if len(cfg.Verify) > 0 {
		fmt.Fprintf(&b, "- The repair is published only if `%s` passes, so run it yourself first.\n", strings.Join(cfg.Verify, " "))
	}
	if strings.TrimSpace(instructions) != "" {
		b.WriteString("\n## Repository instructions\n\n")
		b.WriteString(instructions)
		b.WriteString("\n")
	}
	return b.String()
}

// instructions reads the repository's own guidance from the checkout, bounded,
// so the agent works the way this project expects.
func instructions(cfg Config, directory string) string {
	var b strings.Builder
	for _, name := range cfg.InstructionFiles {
		if !filepath.IsLocal(name) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", name, truncate(strings.TrimSpace(string(body)), 8000))
	}
	return strings.TrimSpace(b.String())
}
