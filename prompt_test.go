package repobot

import (
	"strings"
	"testing"
)

func TestRepairPromptNamesTheFailureAndForbidsWeakeningIt(t *testing.T) {
	cfg := workspace(t)
	cfg.Verify = []string{"make", "check"}
	prompt := repairPrompt(cfg, "main", strings.Repeat("c", 40), []Check{{Name: "build", Conclusion: "failure", Summary: "undefined: Reconcile", URL: "https://example.test/run/1"}}, "### AGENTS.md\n\nRun go test.")
	for _, want := range []string{"acme/orchard", "main", "build", "undefined: Reconcile", "https://example.test/run/1", "make check", "Run go test."} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt omits %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "Never disable, skip, weaken, or delete a check") {
		t.Fatal("prompt must refuse a green branch bought by removing the check")
	}
	if !strings.Contains(prompt, "Do not commit, push, or touch Git history") {
		t.Fatal("publishing is the bot's own step and the prompt must say so")
	}
}

func TestInstructionsStayInsideTheCheckout(t *testing.T) {
	cfg := workspace(t)
	cfg.InstructionFiles = []string{"../escape.md", "AGENTS.md"}
	dir := t.TempDir()
	if err := writeFile(dir+"/AGENTS.md", "Keep it small."); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(dir+"/../escape.md", "Do something else."); err != nil {
		t.Fatal(err)
	}
	text := instructions(cfg, dir)
	if strings.Contains(text, "Do something else.") {
		t.Fatal("instruction files must not reach outside the checkout")
	}
	if !strings.Contains(text, "Keep it small.") {
		t.Fatalf("repository instructions were not read: %q", text)
	}
}
