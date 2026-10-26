# booksy-usage

How to drive `booksy`, the Booksy CLI, from scripts and agents.

## Rules

- Output is JSON on stdout (`--format jsonl` for one object per line). Errors go
  to stderr; pass `--error-format json` for a machine-readable envelope with
  `code`, `category`, `retryable` and `suggested_commands`.
- Exit codes: 0 ok, 1 generic, 2 usage, 3 auth, 4 not found, 5 blocked or rate
  limited, 6 network, 7 Booksy changed its API, 130 interrupted.
- Always pass `--no-input`. Commands that the business sees (`book`,
  `appointment cancel`, `appointment reschedule`) then need `--yes`. Do not pass `--yes` unless the user asked for that exact
  action; prefer `--dry-run` first and show the summary.
- Times are the business's local time, written `2026-10-30T18:00`.

## Find a business and a service

```sh
booksy search barberia --near "Valencia" --limit 5
booksy business show 3349                      # id or booksy.com URL
booksy business services 3349 --format jsonl   # variant ids for --service
booksy business staff 3349                     # staffer ids for --staff
```

`--service` takes a variant id, a service id with one variant, or part of the
service name. An ambiguous name fails with exit 2 and lists the variant ids.

## Availability (no login)

```sh
booksy slots 3349 --service 273164 --days 7 --between 17:00-20:00
booksy slots 3349 --service corte --weekdays sat --first
```

Result: `{business, service, variant_id, from, to, slots: [{date, time, start}]}`.

## Book (login needed, except --dry-run)

```sh
booksy book 3349 --service 273164 --at 2026-10-30T18:00 --dry-run --no-input
booksy book 3349 --service 273164 --at 2026-10-30T18:00 --yes --no-input
booksy book 3349 --service 273164 --first --between 17:00-20:00 --dry-run
```

Result: `{booked, dry_run, draft, summary, appointment}`. A time that is not
open fails with exit 2.

## Appointments

```sh
booksy appointment list --upcoming
booksy appointment show 123456789
booksy appointment cancel 123456789 --yes --no-input
booksy appointment reschedule 123456789 --at 2026-12-05T18:00 --dry-run
```

## Watch for cancellations

```sh
booksy watch add slots 3349 --service 273164 --before 2026-11-07 --notify phone
booksy watch check --format jsonl        # one slots.opened event per watch with news
booksy watch service install --interval 10m
```

A `slots.opened` event carries `data.earliest` and `data.slots`; book one with
`booksy book <data.business_id> --service <data.variant_id> --at <data.earliest.start>`.

## Login

The user logs in on booksy.com and exports cookies, then:

```sh
booksy auth login --cookies ~/Downloads/booksy-cookies.json
booksy auth status --check
```

Never ask the user to paste a token into chat; ask for a file path.
