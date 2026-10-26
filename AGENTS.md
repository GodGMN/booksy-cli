# booksy-cli

Read `CONTEXT.md` for the vocabulary and `docs/cli-spec.md` for behaviour, exit codes and every endpoint. Keep both in sync with code changes.

- Layout: `internal/booksy` is the API client (wire types stay unexported, exported types are the JSON contract), `internal/cli` has one file per noun, `internal/watch` is the pure diff, `internal/store` is SQLite, `internal/fakebooksy` is the test server.
- Commands with effects a business sees or that cannot be undone with one command (`book`, `appointment cancel/reschedule`) must confirm interactively and require `--yes` otherwise. Booking must keep `--dry-run` working without a login. `bookmark add/remove` is private and undone by its opposite, so it runs without a prompt, like the heart button on booksy.com.
- The HTTP client must never follow a redirect off the request's origin: `X-Access-Token` is a custom header and Go re-sends it on every hop.
- Error output and issue links carry no response bodies; `--debug` is the place for those.
- Never put real tokens, cookies, emails, names or appointment data in fixtures, tests, docs or commit messages. The fake server uses invented data.
- Live probes against booksy.com are fine for reads. Anything that would book, cancel or change an account needs the account owner's explicit go-ahead.
- `make check` before committing.
