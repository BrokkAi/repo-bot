# Brokk Repo Bot

`brp` is the repository observer for Brokk Town, and the house that keeps the
branch it covers healthy. It is modeled on the released bug-bot worker
architecture and uses the shared [ACP runner](https://github.com/BrokkAi/acp-go).

It has two duties in one run:

- **Inventory** — one complete observation of the repository: the branch this
  town covers, its exact head, every issue, pull request and release, the
  commits the branch gained since Town last looked, and proof of which commits a
  published release already contains. Town applies that inventory to its own
  task graph; this bot never interprets Town state.
- **Branch health** — read the checks reported on the branch head and, when they
  are failing, repair the branch with an agent and publish the fix.

A partial inventory is never reported as a whole one: a paginated read that
cannot be completed is an error, because Town would otherwise treat what is
missing as gone.

## Repairing a failing branch

When the branch head is red, the bot runs one agent attempt in a private
worktree checked out at that exact revision, and publishes the result only if it
passes the operator's verification command. The commit is fast-forwarded onto
the branch, so a branch that moved while the agent worked is left alone and
observed again on the next run.

Attempts are budgeted per revision and the budget is spent before the agent
starts, so a poll every minute cannot start an agent every minute on the same
red branch, and an attempt that crashes the process is not free. A new failing
revision starts a fresh budget. A push the repository's own protection rules
refuse ends the repair immediately: no further attempt can land it.

The repair prompt names the failing checks and what they reported, and tells the
agent that a check, test or assertion may never be disabled, skipped, weakened
or deleted to reach green. Publishing is the bot's own step; the agent does not
commit, push, or touch Git history.

The bot pushes the repair directly to the branch it covers. Where that branch is
protected, configure Town's merge policy and the branch's own rules
accordingly — this bot does not open a pull request instead.

## Worker protocol

```sh
brp worker --socket PATH
```

The service speaks Brokk Town Worker Protocol v1 over a mode-0600 Unix socket.

- `GET /v1/initialize` identifies `repo-bot` and advertises `run`, `progress`,
  `repo-inventory` and `branch-health`.
- `POST /v1/runs` accepts a strict task. Beyond the shared fields it reads
  `since_head` (the branch head Town last observed) and `commits` (the revisions
  Town still needs release ancestry for).
- Runs return `result.inventory` and `result.health`. The inventory is reported
  even when the health duty fails, because Town's view of the repository must
  not depend on an agent.
- The stream is bounded newline-delimited JSON with progress, terminal result,
  and completion events.

A run with no agent configured reports a failing branch without repairing it.

## Development

```sh
make check
node --test npm/brp.test.cjs
python3 -m unittest discover -s scripts -p '*_test.py'
```
