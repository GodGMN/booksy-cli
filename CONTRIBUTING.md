# Contributing

Read [AGENTS.md](AGENTS.md) for the project rules, [CONTEXT.md](CONTEXT.md) for the vocabulary, and [docs/cli-spec.md](docs/cli-spec.md) for command behavior, exit codes, and endpoints.

## Set up and run

Install Go 1.25.7 or newer and Make, then run from your checkout:

```sh
make build
./bin/booksy --help
```

You can also run commands with `go run ./cmd/booksy`. Searching and checking slots do not need a login, but they contact Booksy.

## Find the code

- `internal/booksy` contains the API client. Wire types stay unexported; exported types define the JSON contract.
- `internal/cli` contains command handling, with one file per noun.
- `internal/watch` contains the pure slot diff.
- `internal/store` contains SQLite persistence.
- `internal/fakebooksy` provides the fake API server for tests.

## Make and verify a change

Add or update tests for changed behavior using the fake server. Tests must not depend on the live Booksy API. Use invented data in fixtures, examples, and bug reports. Never include real credentials or personal or appointment data.

Live reads are allowed. Booking, cancelling, or changing an account requires the account owner's explicit permission. Follow the confirmation, dry-run, redirect, and error-output rules in [AGENTS.md](AGENTS.md).

Keep the specification, vocabulary, and relevant guides in `docs/` and `docs-site/content/docs/` in sync with behavior changes. The site's command reference is generated: edit command help in Go and run `make docs` rather than editing `docs-site/content/docs/reference/` directly.

Before committing or opening a pull request, run:

```sh
make check
```

This runs vet, staticcheck, tests, and a formatting check. Use `make fmt` to format Go code. For command-help changes, also run `make docs-check` to verify the generated reference is committed. CI additionally runs tests with the race detector and builds the binary and documentation site.

## Submit a pull request

Describe the problem, the resulting behavior, and how you verified it. Include a small command or output example when it helps explain the change, using invented data. Keep the change focused so reviewers can follow it.
