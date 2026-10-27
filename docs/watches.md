# Watch for cancellations

A watch remembers which slots were open for one service and tells you when new ones open, which in practice means someone cancelled. Watches live on your machine; Booksy does not know about them. No login needed.

## Create one

```bash
booksy watch add slots 3349 --service 273164 \
  --when "mon-fri 16:00-close" --when sat \
  --before 2026-11-20 --notify phone --name barber
```

It takes the same filters as [`slots`](finding-slots.md) (`--days` defaults to 14), plus:

| flag | meaning |
|---|---|
| `--name` | watch name (default from business and service) |
| `--notify phone,discord` | sinks to send events to (below) |
| `--interval 10m` | check at most this often (default `watch.interval`, else 15m; minimum 2m) |
| `--emit-initial` | report what is open right now, too |

The first check runs immediately and records what is open without reporting it.

## Run checks

```bash
booksy watch check                  # due watches once; prints events
booksy watch check barber           # this one now
booksy watch run --format jsonl     # loop in the foreground until Ctrl-C
booksy watch service install        # systemd user timer (Linux) or launchd agent (macOS)
booksy watch service status
booksy watch events --since 24h --format pretty
booksy watch list --format pretty
booksy watch remove barber
```

On Linux the installer enables `loginctl enable-linger` so the timer keeps running after you log out. On Windows, `service install` prints a `schtasks` command to run.

## What gets reported

Each check that finds newly opened slots emits one `slots.opened` event with the earliest one and the full list:

```json
{"type":"slots.opened","watch":"barber","at":"2026-10-26T20:56:40Z",
 "data":{"business_id":3349,"business":"...","service":"Corte de pelo","variant_id":273164,
         "earliest":{"date":"2026-11-03","time":"18:00","start":"2026-11-03T18:00"},"slots":[...]}}
```

Not reported: slots that appear because the booking window moved forward a day (that would fire every morning). A slot that closes and reopens fires again.

## Notifications (sinks)

Declare sinks in `config.toml` (`booksy config path`), or with `booksy config set sinks.NAME.type ...`:

```toml
[sinks.phone]            # push notification via ntfy.sh or your own ntfy server
type = "ntfy"
url = "https://ntfy.sh"
topic = "pick-a-long-random-topic"

[sinks.discord]          # or template = "slack"; without template the raw event JSON is posted
type = "webhook"
url = "https://discord.com/api/webhooks/..."
template = "discord"

[sinks.script]           # gets the event JSON on stdin, BOOKSY_EVENT_TYPE and BOOKSY_WATCH in env
type = "exec"
command = ["~/bin/on-slot.sh"]
```

```bash
booksy sink list
booksy sink test phone
```

Webhook URLs and ntfy topics work like passwords: `config.toml` is saved with mode 0600, and `sink list` hides them.

If a delivery fails, or the watch names a sink that is no longer in config.toml, the check is not saved and exits non-zero, so the next check reports the same slots again rather than losing them. With `--emit-initial`, a failed first delivery means the watch is not added; fix the sink and run `watch add` again.

## Book from an event

```bash
booksy watch check --format jsonl |
  jq -r '.data | "\(.business_id) \(.variant_id) \(.earliest.start)"' |
  while read b v at; do booksy book "$b" --service "$v" --at "$at"; done
```

`book` still shows the summary and asks; add `--yes` only if you really want unattended booking.
