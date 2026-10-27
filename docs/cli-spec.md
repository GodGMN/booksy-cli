# booksy-cli spec

Status: v1, 2026-10-26. Vocabulary in `../CONTEXT.md`.
Endpoint facts come from the booksy.com marketplace and booking-widget (`/widget-2024/`) bundles and from live probes on that date. The API is unofficial and may drift.

## 1. Name

Binary `booksy`. Module `github.com/GodGMN/booksy-cli`. Go, single static binary, no cgo.

## 2. Usage

```
booksy [global flags] <noun> <verb> [args] [flags]
```

Noun-verb throughout, except `search`, `slots` and `book`, which are top-level because they are what people type most.

## 3. Command tree

```
auth        login | status | logout
profile     list | use | remove
search      [query...]
category    list
place       resolve
business    show | services | staff | hours | reviews | open
slots       <business>
earliest    [treatment text...]
treatment   search
book        <business>
appointment list | show | cancel | reschedule
me          show | reviews
bookmark    list | add | remove
watch       add slots | list | remove | check | run | events | service (install|uninstall|status)
sink        list | test
config      path | list | get | set | unset
doctor
skills      list | get
update
completion  <shell>
```

### update

`update [--check] [--yes]`: reads `GET /repos/GodGMN/booksy-cli/releases/latest`, compares stable `vX.Y.Z` versions, downloads `booksy_{version}_{os}_{arch}.tar.gz` (`.zip` on Windows) and `checksums.txt` as release assets, verifies the SHA-256, extracts only the `booksy` entry, and renames it over the running binary. It asks first (`--yes`, or exit 2 without a terminal). Development builds and `go install` binaries are not replaced; the latter get the `go install` command. A token from `BOOKSY_GITHUB_TOKEN`, `GITHUB_TOKEN`, `GH_TOKEN` or `gh auth token` is sent to api.github.com only (dropped on the redirect to asset storage), which makes a private repository work.

After a successful interactive command (not `update`, `watch`, `auth`, `completion`, `help`), at most once a day (stamp `$XDG_STATE_HOME/booksy-cli/update-check`), a 2-second check prints `booksy: vX is available` on stderr. Failures are silent. `BOOKSY_NO_UPDATE_CHECK=1` disables it.

### Global flags

`--profile NAME` (env `BOOKSY_PROFILE`), `--country CC` (env `BOOKSY_COUNTRY`), `--format json|jsonl|pretty|toon`, `--no-color` (also `NO_COLOR`), `--error-format text|json` (env `BOOKSY_ERROR_FORMAT`), `--no-input`, `--debug` (one stderr line per request; tokens never printed).

Country resolution: flag, `BOOKSY_COUNTRY`, the Profile's login, `profiles.NAME.country`, `country` in config, then `es`. Host is `https://{country}.booksy.com`.

### Business references

A numeric id, a booksy.com business URL or path (`.../3349_slug_...`), or a widget URL carrying `business=ID`.

### Service references (`--service`)

1. A variant id.
2. A service id whose Service has exactly one Variant.
3. A case-insensitive substring of "service name [variant label]". An exact name match wins over substrings. Ambiguity is exit 2 and lists candidate variant ids.

### Staff references (`--staff`)

A staffer id, a case-insensitive name or substring, or `any`. Default any.

### Slot filters (slots, book --first, watch add slots)

`--from YYYY-MM-DD` (not for watches), `--days N` (slots 7, book and watch and earliest 14), `--between HH:MM-HH:MM` (start inclusive, end exclusive), `--weekdays mon,sat | 1-5 | lun,sab`, `--before DATE[THH:MM]`, and `--when` (repeatable, below). All given filters must hold. The booking horizon is the Business's own (seen from one month to over two).

`--when "DAYS [RANGE]"` builds a per-day schedule; a slot must fit one entry:

- DAYS: `mon`..`sun` or `lun`..`dom` or `1`..`7`, lists (`mon,wed`), ranges (`mon-fri`, wrapping `sat-mon`), `daily`, `weekdays`, `weekend`, or dates (`2026-12-24`, `2026-12-24,2026-12-26`).
- RANGE: `HH:MM-HH:MM` (start inclusive, end exclusive). `open` and `close`, or an empty side, mean the start and end of the day; Booksy only offers slots inside opening hours, so they are the business's own hours. Omitted means all day. `off` excludes the days.
- Date entries replace the weekday entries for their date: `--when "mon-fri 16:00-close" --when "2026-12-24 10:00-12:00"` keeps only 10:00 to 12:00 on the 24th.

Watches store the parsed schedule (`filter.windows`).

### earliest

`earliest [TEXT...] [--treatment ID] [--near|--geo|--anywhere] [slot filters] [--days N] [--businesses N] [--per-business N] [--limit N]`

Service names belong to each Business, so matching uses Booksy's catalogue treatment. TEXT resolves through `search/modern_query_hints/` to the first treatment suggestion; the others are printed on stderr. The search is `GET /businesses?treatment=ID` (nearest first when there is a centre), which also returns each Business's services under that treatment. For each of the first `--businesses` (15) results, up to two matching services are queried with their first variant and any staffer, at most 4 businesses at a time. A Business that fails is skipped with a notice. Each Business contributes its earliest `--per-business` (1) slots after the filters, with duplicates of one minute removed. Rows are sorted by start, then distance, and cut to `--limit` (10).

Result: `{treatment, from, to, businesses_checked, results: [{date, time, start, business_id, business, distance_m, address, service, variant_id, duration_min, price, url}]}`. `jsonl` prints the rows. Fees and prepayments are not checked here; `book --dry-run` shows them.

### treatment

`treatment search TEXT`: catalogue treatments matching TEXT, as `[{id, name}]`.

### book

`book <business> --service S (--at TIME | --first) [--staff] [--note] [--dry-run] [--yes]`

1. Without `--dry-run`, require a login (exit 3).
2. Ensure the Profile has a Fingerprint (minted once and saved).
3. `POST drafts/create`, then `change_staffer` if a staffer was asked for and not assigned, then `change_timeslot` (an unavailable time is exit 2), then `add_customer_note`, then `GET confirmation`.
4. `--dry-run` prints `{booked:false, dry_run:true, draft, summary}` and stops.
5. With a login, `POST me/payments/calc/ {draft_id}` prices the draft, as booksy.com's review screen does; the result is `payment {prepayment, cancellation_fee, to_pay_later}`. `prepayment_total` and `cancellation_fee_total` are always decimal strings (`"0.00"` when none); a missing or unreadable one is exit 7 and nothing is booked, since the card check cannot be made.
   - A prepayment above 0 is refused outside `--dry-run` (exit 2, reason `card_required`): paying it needs Stripe's card form.
   - A cancellation fee above 0 (Booksy's no-show protection; the service is still paid at the business) needs a saved card as a guarantee. `GET me/payment_methods/?page=1&per_page=99` lists the cards; the default active, confirmed `credit_card` is used (else the first). It goes into the result as `card {id, brand, last4, default}`, also on `--dry-run`. With no card, a real booking is refused (exit 2, reason `card_required`) and `--dry-run` warns. `appointment reschedule` with a fee is refused (exit 2, `card_required`): moving a guaranteed booking is not mapped yet.
5b. Interactive: the summary goes to stderr and `Book this? [y/N]` is asked. Non-interactive without `--yes` is exit 2. Nothing is booked in either refusal.
6. Without a fee: `POST drafts/{id}/schedule`, then `GET me/appointments/{core_id}/`. Prints `{booked:true, draft, summary, payment, appointment}`.
6b. With a fee: `schedule` cannot attach a card (the appointment stays in status `O` and Booksy cancels it, seen live on 2026-10-26), so the booking goes through `POST me/appointments/business/{business_id}/` as booksy.com does, with the draft's validated time, staffer and variant and the card's `payment_method_id` (body in section 7). Booksy answers with the appointment in status `A` and `payment_info.deposit_id` set: the fee is authorized on the card, charged only for a late cancellation or a no-show. Prints `{booked:true, draft, summary, payment, card, appointment}`.
7. If the appointment comes back as anything but `confirmed`, or a fee booking without a `deposit_id`, the CLI warns on stderr and points to booksy.com or `appointment cancel`. The CLI never sees card numbers, only brand and last digits.

### appointment

- `list [--upcoming|--past] [--limit N]`: newest first. Upcoming means the start is ahead in the Appointment's timezone and the status is not cancelled, finished, no_show, rejected or declined. The server ignores its own `state` parameter, so filtering is client-side.
- `show UID`
- `cancel UID [--yes]`: refuses when Booksy's `actions.cancel` is false. Echoes `_version`.
- `reschedule UID --at TIME [--note] [--dry-run] [--yes]`: `drafts/edit_appointment`, then the same steps as book from 3 on.

### bookmark

`add` and `remove` run without a prompt: they are private to the account and undone by each other, like the heart on booksy.com.

### auth

- `login [--cookies FILE|-] [--cookies-stdin] [--cookies-format auto|json|netscape|header]`: without a file, prompts (TTY only) for a pasted value or a path. Reads `identity-<cc>` cookies and `_bf`, from booksy.com domains only. One identity cookie picks the country unless `--country` is given. `GET /me` validates it before saving. Saves to `credentials.toml` (0600). Seeds `profiles.NAME.country` and, if unset, `profiles.NAME.location` from the account. The first Profile becomes the default. Refused while `BOOKSY_ACCESS_TOKEN` is set.
- `status [--check]`: local metadata. `--check` calls `/me` and adds `session_valid`. Warns when the cookie expires within 14 days.
- `logout [--yes]`: forgets the local login. booksy.com stays logged in.

`BOOKSY_ACCESS_TOKEN` overrides the saved login and is never written.

### watch

- `add slots BUSINESS --service S [filters] [--name] [--notify SINK,...] [--interval D] [--emit-initial]`: resolves names to ids once, stores the target, and runs the first Check immediately (silent baseline unless `--emit-initial`). If that first delivery fails, the watch is removed again and the command fails, since a later check would not be an initial one. Interval: flag, `watch.interval`, else 15m. Floor 2m.
- `check [NAME...] [--all]`: named watches always run. Otherwise only due watches run (interval elapsed, 30s slack). `--all` forces every watch. Prints events. Per-watch failures go to stderr and the first one sets the exit code.
- `run [--interval]`: foreground loop. Backs off 15 minutes on exit-5 class errors.
- `events [NAME] [--since D] [--limit N]`
- `service install|uninstall|status`: systemd user timer (`booksy-watch-PROFILE`) or launchd agent running `watch check --all --profile P --format jsonl`, logging to the state dir. Windows prints a schtasks command.

Check semantics: open = fetched Slots after the filter. New = open, not seen last time, and dated on or before the Horizon (the latest seen date, or last check plus `days` when nothing was seen). One `slots.opened` event per Check with news. Slots no longer open are forgotten, so a reopened Slot fires again. Events go to sinks before the state commit, so a crash re-emits rather than loses. If any delivery fails, or a sink named by the watch is no longer in config.toml, the check is not committed and the command exits non-zero: the next check reports the same slots again (sinks that did get them get a duplicate). A time_slots response without a `time_slots` list (an empty calendar is `[]`) is exit 7 and leaves the watch state alone, so slots are not forgotten and re-reported. Named `check` and `remove` refuse a watch of another profile (exit 4). Two checks of one watch running at the same moment (timer plus manual) can both report the same slots; this is accepted rather than locked.

Event JSON:

```json
{"type":"slots.opened","watch":"barber","profile":"default","at":"2026-10-26T20:56:40Z",
 "data":{"business_id":3349,"business":"...","url":"https://booksy.com/es-es/...","service":"Corte de pelo",
         "variant_id":273164,"staffer":"Javier","earliest":{"date":"2026-10-27","time":"18:00","start":"2026-10-27T18:00"},
         "slots":[...]}}
```

## 4. Files

| file | contents |
|---|---|
| `$XDG_CONFIG_HOME/booksy-cli/config.toml` (or `BOOKSY_CONFIG`) | default_profile, country, language, proxy, api_key, profiles, watch, sinks. 0600, since sink URLs and ntfy topics grant access (older 0644 files are tightened on load). Never login tokens or proxy credentials. Unknown keys are errors. `sink list` hides webhook paths and topics; `config get` shows them. |
| `$XDG_CONFIG_HOME/booksy-cli/credentials.toml` | per-profile access token, fingerprint, account name and email, import time, cookie expiry. 0600. |
| `$XDG_DATA_HOME/booksy-cli/state.db` | SQLite: watches, seen slots, events. 0600. |
| `$XDG_DATA_HOME/booksy-cli/web-api-key` | the web api key, only after Booksy changed it and it was rediscovered |
| `$XDG_STATE_HOME/booksy-cli/` | scheduler logs |

Proxy: `BOOKSY_PROXY` or `proxy` (http, https, socks5, socks5h). Never a direct fallback. Credentials only via the env var.

Profile names match `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`: they end up in file paths and scheduler units.

The HTTP client refuses redirects that change scheme or host, because Go would re-send `X-Access-Token` to the new host.

## 5. Output

JSON on stdout, two-space indented. `jsonl` prints one element per line for list results and one line otherwise. `pretty` uses tab-aligned tables. `toon` is TOON with the JSON field names. Errors go to stderr: text, or with `--error-format json` the envelope `{code, category, retryable, message, reason, http_status, endpoint, suggested_commands, issue_url}`. `reason` is a stable cause when there is one: `card_required` (book refused for a prepayment, or for a cancellation fee with no saved card), `slot_unavailable` (time not open). Endpoints in errors have ids replaced by `{id}`. Issue links never include response bodies.

## 6. Exit codes

| code | category | when |
|---|---|---|
| 0 | | success, including a watch check with no events |
| 1 | generic | Booksy 5xx, doctor failures, anything unclassified |
| 2 | usage | bad flags or arguments, Booksy 4xx validation (for example a closed slot), a refused confirmation |
| 3 | auth | no login, or the token was rejected (401/403 JSON) |
| 4 | not_found | 404, unknown watch, profile, service or staffer |
| 5 | blocked | 429, or a non-JSON 403 (bot protection) |
| 6 | network | connection failures |
| 7 | api_changed | 405, non-JSON success, an unexpected shape, or a web api key that rediscovery could not fix. Prints a prefilled issue link. |
| 130 | | interrupted |

## 7. Endpoints

All requests send `X-Api-Key: web-e3d812bf-d7a2-445d-ab38-55589ae6a121` (booksy.com's public web key), `X-App-Version: 3.0`, a browser User-Agent, and `X-Access-Token` / `X-Fingerprint` / `Accept-Language` when available. Base `https://{cc}.booksy.com/core/v2/customer_api`.

| use | method and path | auth |
|---|---|---|
| categories | `GET /businesses/categories/` | no |
| search | `GET /businesses?response_type=listing_web&query&category&location_geo=lat,lng&location_id&sort_order&businesses_page&businesses_per_page&no_thumbs=true` | no |
| business | `GET /businesses/{id}?no_thumbs=true` | no |
| reviews | `GET /businesses/{id}/reviews?reviews_page&reviews_per_page` | no |
| place | `GET /search/street_hints/resolve/?query=` | no |
| treatments | `GET /search/modern_query_hints/?text=` (suggestions with `object_type: treatment`) | no |
| by treatment | `GET /businesses?treatment=ID&...` (adds `treatment_service_categories`) | no |
| slots | `POST /me/businesses/{id}/appointments/time_slots` `{subbookings:[{service_variant_id,staffer_id}],start_date,end_date}` | no |
| draft | `POST /drafts/create` `{business_id,service_variant_id,staffer_id,meta}` | fingerprint |
| | `POST /drafts/{id}/change_timeslot` `{slot:{d,t},version}` | fingerprint |
| | `POST /drafts/{id}/change_staffer` `{version,subbooking_id,staffer_id}` | fingerprint |
| | `POST /drafts/{id}/add_customer_note` `{customer_note,version}` | fingerprint |
| | `GET /drafts/{id}/confirmation` | fingerprint |
| | `POST /me/payments/calc/` `{draft_id}` | yes |
| | `POST /drafts/{id}/schedule` `{meta}` | yes |
| | `POST /drafts/edit_appointment` `{appointment_id,meta}` | yes |
| | `GET /me/payment_methods/?page=1&per_page=99` (saved cards: `payment_method_id`, `card_type`, `card_last_digits`, `default`, `active`, `confirmed`) | yes |
| | `POST /me/appointments/business/{id}/` `{subbookings:[{staffer_id,booked_from,service_variant:{mode:"variant",id},combo_children:[]}],is_cancellation_fee_preauth_flow:false,pre_auth_type:"control",customer_note,service_questions:[],bci_agreements:{web_communication_agreement:false,disclosure_obligation_agreement:false},ask_for_consent:false,payment_method,compatibilities:{prepayment:true}}` (fee bookings) | yes |
| me | `GET /me` | yes |
| appointments | `GET /me/bookings?bookings_page&bookings_per_page` | yes |
| | `GET /me/appointments/{uid}/?no_thumbs=true&with_combos=1` | yes |
| | `POST /me/appointments/{uid}/action/` `{action:"cancel",_version,no_thumbs}` | yes |
| bookmarks | `GET /me/bookmarks/`, `PUT` / `DELETE /me/bookmarks/{id}/` | yes |
| my reviews | `GET /me/reviews/?reviews_page&reviews_per_page` | yes |

### Web api key

`X-Api-Key` is booksy.com's public web-client key, a constant in its bundle (`{DEVELOPMENT:`web-af3c…`,PRODUCTION:`web-e3d8…`}`). It is not a secret and not per user. Source order: `BOOKSY_API_KEY`, config `api_key`, a key rediscovered earlier (`$XDG_DATA_HOME/booksy-cli/web-api-key`), then the built-in one. `doctor` reports which one is in use.

When Booksy answers `403 {"field":"api_key"}`, the CLI loads `https://booksy.com/{locale}/`, follows its scripts and the chunks they import (at most 600 files), takes the `PRODUCTION` key, saves it, and retries the request once. If that fails, the command exits 7 (api_changed), never 3: the user's login is not the problem.

Draft `meta` is `{app_version:"3.0", platform:"web", device_type:"Desktop", booking_source:"Business_Details"}`.

Verified live on 2026-10-26: everything above, including one real `schedule` (it returned an appointment in status `O` because that business requires a card on file). Not exercised live: `cancel` and bookmark `PUT`/`DELETE`. `edit_appointment` was verified with a dry run that stopped before `schedule`. The card booking (`me/payment_methods/` and `me/appointments/business/{id}/`) was mapped on 2026-10-27 from a real booking made on booksy.com, which came back confirmed with the fee authorized on the saved card.

Seen in the bundles but not used: `me/appointments/wait_list` (Booksy's own waitlist), `drafts/{id}/timeslots`, `drafts/{id}/set_first_available_time_slot`, `drafts/book_again`, `me/appointments/business/{id}/dry_run/`, other payments (`make_payment`, which refuses fee-only bookings, `setup_intent`, `no_show_protection/cancellation_fees/rebind/`), vouchers, stream chat.
