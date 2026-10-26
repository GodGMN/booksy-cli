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

## Schedules: --when

Express the user's availability as `--when` entries instead of running several
queries and merging them. Works on `slots`, `book --first`, `watch add slots`,
`earliest`.

```sh
--when "mon-fri 16:00-close" --when "sat"     # weekday evenings, Saturday all day
--when "weekdays 18:00-" --when "2026-12-24 10:00-12:00" --when "2026-12-31 off"
```

Days: mon..sun (or lun..dom, 1..7), lists, ranges, daily, weekdays, weekend,
or YYYY-MM-DD dates. A date entry replaces the weekday entries for that day.
open/close (or an empty side) mean the business's opening and closing time.

## Earliest slot across businesses

```sh
booksy treatment search barba                    # catalogue ids
booksy earliest --treatment 71 --near "Xàtiva" --when "mon-fri 16:00-close" --when sat
```

Service names are each business's own, so `earliest` matches by catalogue
treatment. With text instead of `--treatment`, stderr says which treatment it
picked; check it is what the user meant. Each row has `business_id`,
`variant_id` and `start`: pass them to `booksy book ... --dry-run` before
booking, since fees only show there.

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

Result: `{booked, dry_run, draft, summary, payment, appointment}`.
`payment` is `{prepayment, cancellation_fee, to_pay_later}` (needs a login).

Failures to handle, both exit 2 with `--error-format json`:

- `reason: "slot_unavailable"`: the time is not open. Re-run `booksy slots`
  and offer the user another time; do not guess times.
- `reason: "card_required"`: the business asks for a prepayment, or uses
  no-show protection (a fee for late cancellation or no-show). Either way
  Booksy needs a card, which only booksy.com or the app can take. Nothing was
  booked. Do not retry with other flags: tell the user to book that one on
  booksy.com or in the app, quoting the amounts from the dry run.

Run `--dry-run` first: a non-zero `payment.prepayment` or
`payment.cancellation_fee` predicts `card_required` before you ask the user
to confirm. Businesses can turn no-show protection on at any time, so a
business that booked fine before can start returning it.

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
