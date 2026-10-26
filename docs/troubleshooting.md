# Troubleshooting and exit codes

Start with:

```bash
booksy doctor --format pretty
booksy <command> --debug      # one stderr line per request; tokens are never printed
```

## Exit codes

| code | meaning | usual fix |
|---|---|---|
| 0 | success (a watch check with no news is success too) | |
| 1 | other failure, e.g. Booksy 5xx, a failed notification | retry later; check `sink test` |
| 2 | your input: bad flag, closed slot, a refused confirmation, a business needing a card | read the message; see `reason` with `--error-format json` |
| 3 | login missing or rejected | `booksy auth login` again |
| 4 | not found: business, appointment, watch, profile, service, staffer | check the id (`business services`, `appointment list`) |
| 5 | Booksy is blocking or rate limiting this address | wait; check less often; a proxy (`BOOKSY_PROXY`) |
| 6 | network | check your connection or proxy |
| 7 | Booksy changed its API | update (`booksy update`); otherwise the error links a prefilled issue |
| 130 | interrupted | |

## Common problems

**"not logged in" or exit 3 after months of use.** The imported cookie expired (about six months). Log in on booksy.com and import again ([auth](auth.md)).

**"logged in to booksy es, not pl".** A profile's login is for one country. Drop `--country`, or log in for the other country under another `--profile`.

**`book` refuses with "no-show protection" or "prepayment".** The business needs a card on file; book that one on booksy.com or in the app ([booking](booking.md)).

**A slot from `slots` is "not available" when booking.** Someone took it in between; run `slots` again.

**`watch service status` says "linger off".** Run `loginctl enable-linger` so the timer survives logout and reboot.

**"the web api key was refused".** booksy.com changed the public key its website uses. The CLI fetches the new one from booksy.com and retries by itself; if even that fails you get exit 7. `api_key` in config or `BOOKSY_API_KEY` override it by hand.

## Reporting a bug

Exit 7 prints a link to a prefilled issue with the command and endpoint, never response bodies. If you add `--debug` output, remove names, emails, phone numbers, notes and addresses first.
