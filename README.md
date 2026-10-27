<img src="assets/icon.svg" width="64" height="64" alt="">

# booksy-cli

Find, watch and book [Booksy](https://booksy.com) appointments from the terminal.

```bash
booksy earliest barba --near Valencia --when "mon-fri 16:00-close" --when sat --format pretty
```
```
DAY         TIME   BUSINESS                        DIST  SERVICE                    PRICE     BUSINESS_ID  VARIANT
Tue 27 Oct  16:00  Barbería Vanellie Capelli       415m  Arreglo de barba Sencillo  12,00 €+  64989        1121918
Tue 27 Oct  16:00  Barbershop Barberia Fabián Fe…  425m  Recorte de la barba        10,00 €+  168103       2360732
...
Recorte de la barba · 15 businesses checked, 2026-10-27 to 2026-11-09 · book with: booksy book BUSINESS_ID --service VARIANT --at DATETIME
```

- **Search** businesses around any place, and read their services, staff, hours and reviews.
- **Open slots** for a service, filtered by your real schedule: `--when "mon-fri 16:00-close" --when sat`.
- **The earliest slot anywhere:** the same kind of service compared across nearby businesses.
- **Watch for cancellations:** get a phone notification (ntfy, Discord, Slack, a script) when an earlier slot opens.
- **Book, reschedule and cancel** from your own account, always with a summary and a confirmation first.
- **Made for scripts and AI agents:** JSON output, stable exit codes, machine-readable errors, `--dry-run`, and a usage guide built into the binary.

One static binary for Linux, macOS and Windows. No login is needed to search, check slots or watch.

## Install

```bash
curl -fsSL https://booksy.gimeno.dev/install.sh | sh
booksy doctor
```

Windows: download the zip from [releases](https://github.com/GodGMN/booksy-cli/releases). With Go: `go install github.com/GodGMN/booksy-cli/cmd/booksy@latest`. Update later with `booksy update`. More in [the install guide](https://booksy.gimeno.dev/docs/install/).

## Quickstart

```bash
# find a business and the variant id of the service you want
booksy search barberia --near "Madrid" --format pretty
booksy business services 3349 --format pretty

# when could you go?
booksy slots 3349 --service "corte de pelo" --when "mon-fri 17:00-close" --when sat --format pretty

# book it (after logging in, see the docs)
booksy book 3349 --service "corte de pelo" --first --when "mon-fri 17:00-close" --dry-run --format pretty

# or get told when something earlier opens up
booksy watch add slots 3349 --service "corte de pelo" --before 2026-11-15 --notify phone
booksy watch service install
```

Output is JSON by default (pipe it into `jq`); `--format pretty` is for people.

## Documentation

Full docs: **https://booksy.gimeno.dev**

| guide | |
|---|---|
| [Install and update](https://booksy.gimeno.dev/docs/install/) | installer, Windows, `booksy update`, uninstall, releasing |
| [Log in](https://booksy.gimeno.dev/docs/auth/) | importing your booksy.com login, profiles, CI |
| [Find businesses and slots](https://booksy.gimeno.dev/docs/finding-slots/) | search, services and variants, `--when` schedules, `earliest` |
| [Book, reschedule and cancel](https://booksy.gimeno.dev/docs/booking/) | the booking flow, prepayments and no-show protection |
| [Watch for cancellations](https://booksy.gimeno.dev/docs/watches/) | watches, the timer, ntfy/webhook/script notifications |
| [Configuration](https://booksy.gimeno.dev/docs/configuration/) | config keys, files, environment variables, countries |
| [Scripts and AI agents](https://booksy.gimeno.dev/docs/agents/) | JSON contract, error envelope, the built-in agent guide |
| [Troubleshooting](https://booksy.gimeno.dev/docs/troubleshooting/) | exit codes and common problems |
| [Specification](docs/cli-spec.md) | every command, flag, file and Booksy endpoint |

## Good to know

- **Unofficial.** booksy-cli uses Booksy's customer web API, the one booksy.com and its booking widget use. It is not made or endorsed by Booksy, and Booksy can change that API at any time; the CLI then exits with code 7 and links a prefilled issue.
- **Your login stays local.** It is imported from your browser's cookies, checked with Booksy, and stored in a file only you can read. Nothing is sent anywhere but Booksy.
- **Cards are never handled.** Businesses that require a prepayment or use Booksy's no-show protection need a card on booksy.com or in the app; `book` stops and tells you so instead of leaving a half-made booking.
- **Nothing happens by surprise.** Booking, rescheduling and cancelling show a summary and ask first. Without a terminal they refuse unless you pass `--yes`.
- **Not a secret.** The `X-Api-Key` in the code is booksy.com's public web-client key that every browser receives. If Booksy changes it, the CLI picks up the new one from booksy.com by itself.
- **Be gentle.** Watches check every 15 minutes by default (2 minutes at the fastest), and `earliest` queries a few businesses at a time.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, the code layout, tests, and pull requests. [AGENTS.md](AGENTS.md) has the project rules and [CONTEXT.md](CONTEXT.md) the vocabulary.

## License

MIT. The structure, output layer and scheduler integration follow [wallapop-cli](https://github.com/Microck/wallapop-cli) and [vinted-cli](https://github.com/Microck/vinted-cli) by Microck (MIT).
