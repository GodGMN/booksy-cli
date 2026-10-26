# Scripts and AI agents

The CLI is built to be driven by programs:

- stdout is JSON (`--format jsonl` for one object per line, `toon` for compact LLM input); stderr has notices and errors
- `--error-format json` makes errors a JSON envelope: `code`, `category`, `reason`, `retryable`, `message`, `suggested_commands`
- exit codes are stable (see [troubleshooting](troubleshooting.md))
- `--no-input` never prompts; anything that changes an account then requires `--yes`
- `--dry-run` on `book` and `reschedule` shows exactly what would happen

The guide written for agents ships inside the binary, so it always matches the installed version:

```bash
booksy skills get booksy-usage
```

Its source is [internal/cli/skills/booksy-usage.md](../internal/cli/skills/booksy-usage.md). Point your agent at it, for example in a system prompt: "Before using booksy, run `booksy skills get booksy-usage` and follow it."

Rules worth repeating:

1. Run `book --dry-run` and show the user the summary before booking; pass `--yes` only when the user asked for that exact booking.
2. `reason: "card_required"` means the business needs a card (prepayment or no-show protection). Nothing was booked; send the user to booksy.com or the app instead of retrying.
3. `reason: "slot_unavailable"`: re-run `slots` and offer real times; never guess.
4. Use `--when` for the user's availability rather than several queries merged by hand.
5. Never ask a user to paste their cookie into a chat; ask for a file path for `auth login --cookies`.
