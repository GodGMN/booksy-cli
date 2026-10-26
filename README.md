<img src="assets/icon.svg" width="64" height="64" alt="">

# booksy-cli

`booksy` is a terminal client for [Booksy](https://booksy.com): search businesses, check open slots, get told when an earlier appointment opens up, and book, reschedule or cancel from your own account. It compiles to one static Go binary. Output is JSON by default so everything pipes into `jq`; `--format pretty` is for humans.

It talks to Booksy's unofficial customer web API, the same one booksy.com and its booking widget use. Nothing here is endorsed by Booksy, and the API can change without notice.

## why

The barber you want is booked out for three weeks, but people cancel all the time. `booksy watch` checks the calendar for you and pings your phone the moment a slot you'd take opens, and `booksy book` grabs it in one command.

## install

```bash
go install github.com/GodGMN/booksy-cli/cmd/booksy@latest
booksy --version
booksy doctor
```

or from a clone: `make install`.

## quickstart

No login needed to look around:

```bash
booksy search barberia --near "Valencia" --format pretty
booksy business services 3349 --format pretty          # variant ids for --service
booksy slots 3349 --service "corte de pelo" --between 17:00-20:00 --format pretty
booksy book 3349 --service "corte de pelo" --first --dry-run --format pretty
```

`--service` takes a variant id, a service id that has one variant, or part of the service name. Business arguments take the numeric id or the booksy.com URL.

## auth

booksy.com keeps your login in a cookie called `identity-<country>` (`identity-es` in Spain), valid for about six months. The CLI imports it instead of asking for a password, so email, Google, Apple and Facebook logins all work the same way.

1. log in at booksy.com in your browser
2. export cookies for booksy.com with an extension such as Cookie-Editor (JSON), or copy the `identity-es` value
3. import it:

```bash
booksy auth login --cookies ~/Downloads/booksy-cookies.json
booksy auth status --check
```

Netscape `cookies.txt`, Cookie-Editor/EditThisCookie JSON, a `Cookie:` header and the bare value all work, and only booksy.com cookies are read. The token is checked against Booksy, then stored in `credentials.toml` (mode 0600), never in `config.toml`. For CI, set `BOOKSY_ACCESS_TOKEN` instead and nothing is written to disk.

| credential | unlocks |
|---|---|
| none | `search`, `category`, `place`, `business *`, `slots`, `book --dry-run`, watches |
| login | `book`, `appointment *`, `me *`, `bookmark *` |

## watch for cancellations

```bash
booksy watch add slots 3349 --service "corte de pelo" --staff Javier \
  --between 17:00-20:00 --before 2026-11-07 --notify phone
booksy watch check --format jsonl            # one-shot; prints slots.opened events
booksy watch service install --interval 10m  # systemd timer or launchd agent
```

The first check records what is open without reporting it. After that, each check that finds newly opened slots emits one `slots.opened` event with the earliest slot and the full list, and sends it to the watch's sinks. Slots that appear only because the booking window rolled forward a day are not reported.

Sinks live in `config.toml`:

```toml
[sinks.phone]
type = "ntfy"
url = "https://ntfy.sh"
topic = "my-secret-topic"

[sinks.discord]
type = "webhook"
url = "https://discord.com/api/webhooks/..."
template = "discord"

[sinks.script]
type = "exec"
command = ["~/bin/on-slot.sh"]   # gets the event JSON on stdin
```

Book straight from an event:

```bash
booksy watch check --format jsonl | jq -r '.data | "\(.business_id) \(.variant_id) \(.earliest.start)"' |
  while read b v at; do booksy book "$b" --service "$v" --at "$at"; done
```

## command surface

| command | what it does |
|---|---|
| `search [query] --near/--geo --category --sort` | find businesses |
| `category list`, `place resolve TEXT` | category ids, what `--near` resolves to |
| `business show / services / staff / hours / reviews / open` | inspect a business |
| `slots BUSINESS --service ...` | open start times (`--days --between --weekdays --before --staff --first`) |
| `book BUSINESS --service ... --at TIME \| --first` | book, with confirmation (`--dry-run`, `--yes`, `--note`) |
| `appointment list / show / cancel / reschedule` | your appointments |
| `me show / reviews`, `bookmark list / add / remove` | your account |
| `watch add slots / list / remove / check / run / events / service` | cancellation alerts |
| `sink list / test` | notification targets |
| `auth login / status / logout`, `profile list / use / remove` | logins, several accounts or countries |
| `config path / list / get / set / unset`, `doctor`, `skills list / get` | housekeeping |

Global flags: `--profile`, `--country` (es, us, pl, gb, ...; default es), `--format json|jsonl|pretty|toon`, `--no-color`, `--error-format text|json`, `--no-input`, `--debug`.

Anything that changes your account (`book`, `appointment cancel/reschedule`) shows what it will do and asks first. Without a terminal it refuses unless you pass `--yes`.

## exit codes

`0` ok, `1` generic, `2` usage, `3` auth, `4` not found, `5` blocked or rate limited, `6` network, `7` Booksy changed its API (the error prints a prefilled issue link), `130` interrupted.

## more

- [docs/cli-spec.md](docs/cli-spec.md): the full spec, including every endpoint used
- [CONTEXT.md](CONTEXT.md): vocabulary
- `booksy skills get booksy-usage`: a guide for agents and scripts

The structure, output layer and scheduler integration follow [wallapop-cli](https://github.com/Microck/wallapop-cli) and [vinted-cli](https://github.com/Microck/vinted-cli) (MIT).
