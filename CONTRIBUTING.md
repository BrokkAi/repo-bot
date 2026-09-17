# Contributing to Brokk Repo Bot

Contributions from people using AI tools are welcome. Everyone remains
responsible for the accuracy, safety, licensing, and relevance of their work.
Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Issues and pull requests

Search existing issues and pull requests before opening a new one. For bugs,
include the version or commit, operating system, reproduction steps, expected
behavior, and actual behavior. Redact secrets and private source code from logs
and transcripts. Report vulnerabilities privately as described in
[SECURITY.md](SECURITY.md).

Keep changes focused. Discuss substantial behavior or interface changes with
maintainers before implementing them. A pull request should explain the
problem, resulting behavior, validation performed, and any remaining limits.
Link related issues and update documentation when behavior changes.

## Development and validation

Install the Go version in `go.mod` and Python 3. Run from the repository root:

```sh
go test -race ./...
go vet ./...
python3 -m unittest discover -s scripts -p '*_test.py'
```

Format Go changes with `gofmt`. Add focused tests for behavior changes; ordinary
documentation changes need a diff and link review. Tests should use temporary
repositories and simulated agents, without publishing releases or requiring
live credentials.

For installer and release changes, also use Node.js 24 and run the local
packaging smoke checks described in [RELEASING.md](RELEASING.md).

## Licensing and dependencies

This project uses [MIT](LICENSE). By intentionally submitting a
contribution for inclusion, you submit it under the project's license unless
you explicitly state otherwise. Submit only work
you have the right to share and preserve upstream attribution and notices.

Release packaging generates third-party notices automatically from the selected
Go modules and toolchain. No checked-in notice inventory, hash updates, or
separate approvals are needed for dependency upgrades. Commit `go.mod` and
`go.sum` when dependencies change. Do not add local replacement directives to
a release. See [RELEASING.md](RELEASING.md).
