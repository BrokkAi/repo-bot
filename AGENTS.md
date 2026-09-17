# AGENTS.md

Repo Bot is a local Go/ACP worker for Brokk Town. Keep repository and GitHub I/O
off render loops. Use exact revisions, private worktrees, bounded output,
cancellation, and durable attempt budgets. Never infer a successful GitHub write
from process exit or a zero-length response, and never report a paginated read
that did not complete as a whole inventory.

A repair must fix the cause. Never disable, skip, weaken or delete a check, a
test, or an assertion to make a branch green, and never publish one that failed
the operator's verification command.

Run `go test -race ./...`, `go vet ./...`, `node --test npm/brp.test.cjs`, and
`python3 -m unittest discover -s scripts -p '*_test.py'`. Use fake agents and
GitHub responses for tests; never run live repository automation as a
development test.

Keep credentials out of logs, snapshots, and source control. Commit coherent,
validated changes. Do not publish a release unless explicitly requested.
