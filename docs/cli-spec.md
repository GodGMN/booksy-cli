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
book        <business>
appointment list | show | cancel | reschedule
me          show | reviews
bookmark    list | add | remove
watch       add slots | list | remove | check | run | events | service (install|uninstall|status)
sink        list | test
config      path | list | get | set | unset
doctor
skills      list | get
completion  <shell>
```

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

`--from YYYY-MM-DD` (not for watches), `--days N` (slots 7, book 14, watch 14), `--between HH:MM-HH:MM` (start inclusive, end exclusive), `--weekdays mon,sat | 1-5 | lun,sab`, `--before DATE[THH:MM]`. Booksy caps the window at the Business's booking horizon (about a month).

### book

`book <business> --service S (--at TIME | --first) [--staff] [--note] [--dry-run] [--yes]`

1. Without `--dry-run`, require a login (exit 3).
2. Ensure the Profile has a Fingerprint (minted once and saved).
3. `POST drafts/create`, then `change_staffer` if a staffer was asked for and not assigned, then `change_timeslot` (an unavailable time is exit 2), then `add_customer_note`, then `GET confirmation`.
4. `--dry-run` prints `{booked:false, dry_run:true, draft, summary}` and stops.
5. Interactive: the summary goes to stderr and `Book this? [y/N]` is asked. Non-interactive without `--yes` is exit 2. Nothing is booked in either refusal.
6. `POST drafts/{id}/schedule`, then `GET me/appointments/{core_id}/`. Prints `{booked:true, draft, summary, appointment}`.
7. A business with no-show protection or prepayment leaves the appointment in status `awaiting_payment` (Booksy `O`) until a card is attached through booksy.com's Stripe form. The CLI does not handle cards; it warns on stderr and points to booksy.com or `appointment cancel`.

### appointment

- `list [--upcoming|--past] [--limit N]`: newest first. Upcoming means the start is ahead in the Appointment's timezone and the status is not cancelled, finished, no_show, rejected or declined. The server ignores its own `state` parameter, so filtering is client-side.
- `show UID`
- `cancel UID [--yes]`: refuses when Booksy's `actions.cancel` is false. Echoes `_version`.
- `reschedule UID --at TIME [--note] [--dry-run] [--yes]`: `drafts/edit_appointment`, then the same steps as book from 3 on.

### auth

- `login [--cookies FILE|-] [--cookies-stdin] [--cookies-format auto|json|netscape|header]`: without a file, prompts (TTY only) for a pasted value or a path. Reads `identity-<cc>` cookies and `_bf`, from booksy.com domains only. One identity cookie picks the country unless `--country` is given. `GET /me` validates it before saving. Saves to `credentials.toml` (0600). Seeds `profiles.NAME.country` and, if unset, `profiles.NAME.location` from the account. The first Profile becomes the default. Refused while `BOOKSY_ACCESS_TOKEN` is set.
- `status [--check]`: local metadata. `--check` calls `/me` and adds `session_valid`. Warns when the cookie expires within 14 days.
- `logout [--yes]`: forgets the local login. booksy.com stays logged in.

`BOOKSY_ACCESS_TOKEN` overrides the saved login and is never written.

### watch

- `add slots BUSINESS --service S [filters] [--name] [--notify SINK,...] [--interval D] [--emit-initial]`: resolves names to ids once, stores the target, and runs the first Check immediately (silent baseline unless `--emit-initial`). Interval: flag, `watch.interval`, else 15m. Floor 2m.
- `check [NAME...] [--all]`: named watches always run. Otherwise only due watches run (interval elapsed, 30s slack). `--all` forces every watch. Prints events. Per-watch failures go to stderr and the first one sets the exit code.
- `run [--interval]`: foreground loop. Backs off 15 minutes on exit-5 class errors.
- `events [NAME] [--since D] [--limit N]`
- `service install|uninstall|status`: systemd user timer (`booksy-watch-PROFILE`) or launchd agent running `watch check --all --profile P --format jsonl`, logging to the state dir. Windows prints a schtasks command.

Check semantics: open = fetched Slots after the filter. New = open, not seen last time, and dated on or before the Horizon (the latest seen date, or last check plus `days` when nothing was seen). One `slots.opened` event per Check with news. Slots no longer open are forgotten, so a reopened Slot fires again. Events go to sinks before the state commit, so a crash re-emits rather than loses.

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
| `$XDG_CONFIG_HOME/booksy-cli/config.toml` (or `BOOKSY_CONFIG`) | default_profile, country, language, proxy, profiles, watch, sinks. World-readable, never secrets. Unknown keys are errors. |
| `$XDG_CONFIG_HOME/booksy-cli/credentials.toml` | per-profile access token, fingerprint, account name and email, import time, cookie expiry. 0600. |
| `$XDG_DATA_HOME/booksy-cli/state.db` | SQLite: watches, seen slots, events. 0600. |
| `$XDG_DATA_HOME/booksy-cli/web-api-key` | the web api key, only after Booksy changed it and it was rediscovered |
| `$XDG_STATE_HOME/booksy-cli/` | scheduler logs |

Proxy: `BOOKSY_PROXY` or `proxy` (http, https, socks5, socks5h). Never a direct fallback. Credentials only via the env var.

## 5. Output

JSON on stdout, two-space indented. `jsonl` prints one element per line for list results and one line otherwise. `pretty` uses tab-aligned tables. `toon` is TOON with the JSON field names. Errors go to stderr: text, or with `--error-format json` the envelope `{code, category, retryable, message, http_status, endpoint, suggested_commands, issue_url}`. Endpoints in errors have ids replaced by `{id}`.

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
| slots | `POST /me/businesses/{id}/appointments/time_slots` `{subbookings:[{service_variant_id,staffer_id}],start_date,end_date}` | no |
| draft | `POST /drafts/create` `{business_id,service_variant_id,staffer_id,meta}` | fingerprint |
| | `POST /drafts/{id}/change_timeslot` `{slot:{d,t},version}` | fingerprint |
| | `POST /drafts/{id}/change_staffer` `{version,subbooking_id,staffer_id}` | fingerprint |
| | `POST /drafts/{id}/add_customer_note` `{customer_note,version}` | fingerprint |
| | `GET /drafts/{id}/confirmation` | fingerprint |
| | `POST /drafts/{id}/schedule` `{meta}` | yes |
| | `POST /drafts/edit_appointment` `{appointment_id,meta}` | yes |
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

Verified live on 2026-10-26: everything above, including one real `schedule` (it returned an appointment in status `O` because that business requires a card on file). Not exercised live: `cancel` and bookmark `PUT`/`DELETE`. `edit_appointment` was verified with a dry run that stopped before `schedule`.

Seen in the bundles but not used: `me/appointments/wait_list` (Booksy's own waitlist), `drafts/{id}/timeslots`, `drafts/{id}/set_first_available_time_slot`, `drafts/book_again`, payments, vouchers, stream chat.
