package repobot

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fakeGitHub puts a `gh` on PATH that answers the endpoint it is given from a
// table of shell glob patterns. An endpoint no pattern covers is an error, so a
// test can never pass on a request nobody meant to answer.
func fakeGitHub(t *testing.T, answers map[string]string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "brp-gh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	// The most specific pattern wins, whatever order the table was written in.
	patterns := make([]string, 0, len(answers))
	for pattern := range answers {
		patterns = append(patterns, pattern)
	}
	sort.Slice(patterns, func(i, j int) bool { return len(patterns[i]) > len(patterns[j]) })
	var script strings.Builder
	script.WriteString("#!/bin/sh\nfor endpoint; do :; done\ncase \"$endpoint\" in\n")
	for _, pattern := range patterns {
		script.WriteString("  " + pattern + ")\n    cat <<'JSON'\n" + answers[pattern] + "\nJSON\n    ;;\n")
	}
	script.WriteString("  *)\n    echo \"unexpected endpoint: $endpoint\" >&2\n    exit 1\n    ;;\nesac\n")
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script.String()), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const (
	headSHA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mergedSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func repositoryAnswers() map[string]string {
	return map[string]string{
		"repos/acme/orchard":               `{"default_branch":"main"}`,
		"repos/acme/orchard/branches/main": `{"commit":{"sha":"` + headSHA + `"}}`,
		"*page=1":                          `[]`,
		"*/issues\\?*":                     `[{"number":7,"title":"Broken build","body":"","html_url":"https://github.com/acme/orchard/issues/7","state":"open","locked":false,"updated_at":"2026-09-01T00:00:00Z"}]`,
		"*/pulls\\?*":                      `[{"number":8,"title":"Fix it","html_url":"https://github.com/acme/orchard/pull/8","state":"open","head":{"ref":"fix","sha":"` + mergedSHA + `"},"base":{"ref":"main","sha":"` + headSHA + `"},"merged_at":null,"updated_at":"2026-09-02T00:00:00Z"}]`,
		"*/releases\\?*":                   `[{"tag_name":"v1.0.0","name":"1.0.0","html_url":"https://github.com/acme/orchard/releases/tag/v1.0.0","draft":false,"prerelease":false,"published_at":"2026-08-01T00:00:00Z"}]`,
	}
}

func inventoryConfig(t *testing.T) Config {
	cfg := workspace(t)
	cfg.Branch = "main"
	return cfg
}

func TestSnapshotReadsTheWholeRepository(t *testing.T) {
	answers := repositoryAnswers()
	fakeGitHub(t, answers)
	inventory, err := (githubClient{config: inventoryConfig(t)}).snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Head != headSHA || inventory.Branch != "main" || inventory.DefaultBranch != "main" {
		t.Fatalf("unexpected branch state: %+v", inventory)
	}
	if len(inventory.Issues) != 1 || inventory.Issues[0].Number != 7 {
		t.Fatalf("issues = %+v", inventory.Issues)
	}
	if len(inventory.Pulls) != 1 || inventory.Pulls[0].Base.Ref != "main" {
		t.Fatalf("pulls = %+v", inventory.Pulls)
	}
	if len(inventory.Releases) != 1 || inventory.Releases[0].Tag != "v1.0.0" {
		t.Fatalf("releases = %+v", inventory.Releases)
	}
}

func TestSnapshotRefusesABranchWithoutAUsableHead(t *testing.T) {
	answers := repositoryAnswers()
	answers["repos/acme/orchard/branches/main"] = `{"commit":{"sha":""}}`
	fakeGitHub(t, answers)
	if _, err := (githubClient{config: inventoryConfig(t)}).snapshot(context.Background()); err == nil {
		t.Fatal("an inventory without a branch head must not be reported as one")
	}
}

func TestChecksClassifyTheReportedVerdict(t *testing.T) {
	cases := []struct {
		name   string
		runs   string
		status string
		want   string
		fails  int
	}{
		{"green", `{"total_count":1,"check_runs":[{"name":"build","status":"completed","conclusion":"success"}]}`, `{"state":"success","statuses":[]}`, healthGreen, 0},
		{"red", `{"total_count":1,"check_runs":[{"name":"build","status":"completed","conclusion":"failure","output":{"title":"build failed","summary":"undefined: Reconcile"}}]}`, `{"state":"success","statuses":[]}`, healthRed, 1},
		{"pending", `{"total_count":1,"check_runs":[{"name":"build","status":"in_progress"}]}`, `{"state":"pending","statuses":[]}`, healthPending, 0},
		{"unreported", `{"total_count":0,"check_runs":[]}`, `{"state":"pending","statuses":[]}`, healthUnreported, 0},
		{"failed status", `{"total_count":0,"check_runs":[]}`, `{"state":"failure","statuses":[{"context":"ci/legacy","state":"failure","description":"exit 1","target_url":"https://example.test/1"}]}`, healthRed, 1},
		{"a skipped check is not a failure", `{"total_count":2,"check_runs":[{"name":"build","status":"completed","conclusion":"success"},{"name":"slow","status":"completed","conclusion":"skipped"}]}`, `{"state":"success","statuses":[]}`, healthGreen, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeGitHub(t, map[string]string{
				"*/check-runs\\?*":   c.runs,
				"*/commits/*/status": c.status,
			})
			got, err := (githubClient{config: inventoryConfig(t)}).checks(context.Background(), headSHA)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != c.want || len(got.Failing) != c.fails {
				t.Fatalf("state = %q with %d failing, want %q with %d", got.State, len(got.Failing), c.want, c.fails)
			}
		})
	}
}

func TestRunReportsInventoryAndReleaseAncestry(t *testing.T) {
	answers := repositoryAnswers()
	// The branch carries nothing beyond the release, so the merged commit is
	// proven individually rather than assumed released by its absence.
	answers["*/compare/v1.0.0...*"] = `{"status":"ahead","commits":[]}`
	answers["*/compare/"+mergedSHA+"...*"] = `{"status":"ahead"}`
	answers["*/check-runs\\?*"] = `{"total_count":1,"check_runs":[{"name":"build","status":"completed","conclusion":"success"}]}`
	answers["*/commits/*/status"] = `{"state":"success","statuses":[]}`
	fakeGitHub(t, answers)
	cfg := inventoryConfig(t)
	result, err := Run(context.Background(), cfg, Request{Commits: []string{mergedSHA}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inventory == nil || result.Inventory.Head != headSHA {
		t.Fatalf("inventory = %+v", result.Inventory)
	}
	if !result.Inventory.Released[mergedSHA] {
		t.Fatalf("release ancestry was not settled: %+v", result.Inventory.Released)
	}
	if result.Health == nil || result.Health.State != healthGreen {
		t.Fatalf("health = %+v", result.Health)
	}
}

func TestRunReportsTheInventoryEvenWhenTheHealthDutyFails(t *testing.T) {
	answers := repositoryAnswers()
	answers["*/check-runs\\?*"] = ""
	fakeGitHub(t, answers)
	result, err := Run(context.Background(), inventoryConfig(t), Request{}, nil, nil)
	if err == nil {
		t.Fatal("a check query that fails must be reported as a failure")
	}
	if result.Inventory == nil || result.Inventory.Head != headSHA {
		t.Fatalf("Town's view of the repository must not depend on the health duty: %+v", result.Inventory)
	}
}
