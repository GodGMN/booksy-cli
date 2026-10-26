# Configuration, profiles and countries

```bash
booksy config path        # where config, credentials and state live
booksy config list
booksy config set country pl
booksy config get watch.interval
booksy config unset proxy
```

| key | meaning |
|---|---|
| `default_profile` | profile used without `--profile` |
| `country` | default country: `es`, `us`, `pl`, `gb`, `br`, ... (default `es`) |
| `language` | `Accept-Language` sent to Booksy, e.g. `en-US`; changes service names and summaries |
| `proxy` | `http://`, `https://`, `socks5://` or `socks5h://` URL; never a direct fallback; no credentials |
| `watch.interval` | default watch interval, e.g. `10m` |
| `profiles.NAME.country` | a profile's country |
| `profiles.NAME.location.lat` / `.lng` / `.label` | default search centre |
| `sinks.NAME.*` | notification targets, see [watches](watches.md) |
| `api_key` | override Booksy's public web key (rarely needed; see troubleshooting) |

## Files

| file | contents |
|---|---|
| `~/.config/booksy-cli/config.toml` | the keys above, mode 0600 |
| `~/.config/booksy-cli/credentials.toml` | saved logins and device ids, mode 0600 |
| `~/.local/share/booksy-cli/state.db` | watches and their history (SQLite) |
| `~/.local/state/booksy-cli/` | timer logs, the daily update stamp |

`XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME` move them; `BOOKSY_CONFIG` moves only `config.toml`.

## Environment

| variable | effect |
|---|---|
| `BOOKSY_PROFILE`, `BOOKSY_COUNTRY` | same as `--profile`, `--country` |
| `BOOKSY_ACCESS_TOKEN` | login for this run only, never saved |
| `BOOKSY_PROXY` | proxy, may carry credentials |
| `BOOKSY_ERROR_FORMAT=json` | same as `--error-format json` |
| `BOOKSY_NO_UPDATE_CHECK=1` | no daily update notice |
| `NO_COLOR` | no colour in pretty output |

## Profiles

A profile is one login in one country. `booksy profile list`, `profile use NAME`, `profile remove NAME` (also removes its watches). Names may use letters, digits, `.`, `_` and `-`.

## Countries

Booksy runs a separate site per country; `--country` (or the profile's country) picks `https://{country}.booksy.com`. Business ids, categories and treatments differ between countries.
