# Install and update

## Linux and macOS

```bash
curl -fsSL https://raw.githubusercontent.com/GodGMN/booksy-cli/main/scripts/install.sh | sh
booksy --version
booksy doctor
```

The script picks the release archive for your OS and CPU (amd64 or arm64), checks it against the release's `checksums.txt`, and installs `booksy` into `~/.local/bin`. If that directory is not on your `PATH`, it tells you the line to add.

| variable | effect |
|---|---|
| `BOOKSY_INSTALL_DIR=/usr/local/bin` | install somewhere else |
| `BOOKSY_VERSION=v0.1.0` | install a specific version |

If you have a logged-in `gh` CLI, the script downloads through it.

## Windows

Download `booksy_<version>_windows_amd64.zip` (or `arm64`) from the [releases page](https://github.com/GodGMN/booksy-cli/releases), unzip it, and put `booksy.exe` in a folder on your `PATH`.

## With Go

```bash
go install github.com/GodGMN/booksy-cli/cmd/booksy@latest
```

or, from a clone, `make install`.

## Check it works

```bash
booksy doctor --format pretty
```

`doctor` checks the config, that Booksy answers, the web API key, your login (if any), the state database, your sinks and the watch timer.

## Update

```bash
booksy update --check   # report only
booksy update           # download, verify, replace; asks first
booksy update --yes     # no question
```

`update` verifies the new archive's SHA-256 against `checksums.txt` before touching anything, and replaces the binary with an atomic rename. Copies installed with `go install` are told to update with `go install`; development builds are never replaced.

Interactive commands print one line on stderr, at most once a day, when a newer release exists. Scripts and `--no-input` runs never check. `BOOKSY_NO_UPDATE_CHECK=1` turns the notice off.

## Uninstall

```bash
booksy watch service uninstall   # if you installed the timer
rm ~/.local/bin/booksy
rm -r ~/.config/booksy-cli ~/.local/share/booksy-cli ~/.local/state/booksy-cli
```

The last line deletes your saved login, watches and settings (`booksy config path` shows where they are).

## Releasing (maintainers)

Push a version tag; GitHub Actions runs the tests and goreleaser, and publishes archives for Linux, macOS and Windows on amd64 and arm64 plus `checksums.txt`.

```bash
git tag v0.2.0 && git push origin v0.2.0
make snapshot   # the same build locally into dist/, nothing published
```
