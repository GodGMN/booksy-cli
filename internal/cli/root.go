// Package cli wires cobra commands to the booksy client, the store and the
// output layer. One file per noun. Command handlers stay thin: parse flags,
// call the client, print.
package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/config"
	"github.com/GodGMN/booksy-cli/internal/output"
	"github.com/GodGMN/booksy-cli/internal/store"
)

// App is the per-invocation state shared by every command.
type App struct {
	Version string
	Paths   config.Paths
	Cfg     config.Config
	Creds   config.Credentials
	Profile string
	Country string
	Client  *booksy.Client
	Printer output.Printer

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// Interactive is true only when stdin, stdout and stderr are all TTYs and
	// --no-input is absent. Prompts happen only then.
	Interactive bool

	// tokenFromEnv is set when BOOKSY_ACCESS_TOKEN supplies the session, which
	// is never written to disk.
	tokenFromEnv bool
	// keySource says where the web api key came from, for doctor and notices.
	keySource string

	flagProfile     string
	flagCountry     string
	flagFormat      string
	flagNoColor     bool
	flagErrorFormat string
	flagNoInput     bool
	flagDebug       bool

	store *store.Store
}

// Execute runs the CLI and returns the process exit code.
func Execute(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	app := &App{Version: version, Stdin: stdin, Stdout: stdout, Stderr: stderr}
	root := app.rootCmd()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, err := root.ExecuteContextC(ctx)
	if app.store != nil {
		app.store.Close()
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		fmt.Fprintln(stderr, "booksy: interrupted")
		return 130
	}
	path := "booksy"
	if cmd != nil {
		path = cmd.CommandPath()
	}
	ef := output.ErrorFormat(app.flagErrorFormat)
	if v := os.Getenv("BOOKSY_ERROR_FORMAT"); v != "" && app.flagErrorFormat == "text" {
		ef = output.ErrorFormat(v)
	}
	return output.PrintError(stderr, err, ef, version, path)
}

func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "booksy",
		Short: "Find, watch and book Booksy appointments from the terminal",
		Long: `Find businesses, check open slots, get notified when an earlier one opens,
and book or cancel appointments on Booksy, from the terminal.

Output is JSON by default so every command pipes into jq. Use --format pretty
for humans. Errors go to stderr; exit codes are listed in docs/cli-spec.md.

Examples:
  booksy search barberia --near Valencia --format pretty
  booksy business services 3349 --format pretty
  booksy slots 3349 --service "corte" --between 17:00-20:00 --format pretty
  booksy watch add slots 3349 --service "corte" --before 2026-11-01 --notify phone
  booksy auth login --cookies ~/Downloads/booksy-cookies.json
  booksy book 3349 --service "corte" --at 2026-10-30T18:00

Agent usage: booksy skills get booksy-usage`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       a.Version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return a.setup(cmd)
		},
	}
	root.SetVersionTemplate("booksy {{.Version}}\n")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return output.Usagef("%s. See `%s --help`", err.Error(), cmd.CommandPath())
	})

	pf := root.PersistentFlags()
	pf.StringVar(&a.flagProfile, "profile", "", "profile to act as (env BOOKSY_PROFILE)")
	pf.StringVar(&a.flagCountry, "country", "", "Booksy country: es, us, pl, gb, ... (env BOOKSY_COUNTRY; default from profile, else es)")
	pf.StringVar(&a.flagFormat, "format", "json", "output format: json, jsonl, pretty, toon")
	pf.BoolVar(&a.flagNoColor, "no-color", false, "disable colour in pretty output (also NO_COLOR)")
	pf.StringVar(&a.flagErrorFormat, "error-format", "text", "error rendering on stderr: text or json")
	pf.BoolVar(&a.flagNoInput, "no-input", false, "never prompt; fail when input is missing")
	pf.BoolVar(&a.flagDebug, "debug", false, "log every http request to stderr (secrets redacted)")
	_ = root.RegisterFlagCompletionFunc("format", cobra.FixedCompletions([]string{"json", "jsonl", "pretty", "toon"}, cobra.ShellCompDirectiveNoFileComp))
	_ = root.RegisterFlagCompletionFunc("error-format", cobra.FixedCompletions([]string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp))

	root.AddCommand(
		a.authCmd(), a.profileCmd(),
		a.searchCmd(), a.categoryCmd(), a.placeCmd(),
		a.businessCmd(), a.slotsCmd(),
		a.bookCmd(), a.appointmentCmd(),
		a.meCmd(), a.bookmarkCmd(),
		a.watchCmd(), a.sinkCmd(),
		a.configCmd(), a.doctorCmd(), a.skillsCmd(),
	)
	return root
}

// setup loads config and credentials and builds the client. It runs for every
// command, so it must stay cheap: no network.
func (a *App) setup(cmd *cobra.Command) error {
	if cmd.Name() == "help" || cmd.Name() == "completion" || (cmd.Parent() != nil && cmd.Parent().Name() == "completion") {
		return nil
	}
	format, err := output.ParseFormat(a.flagFormat)
	if err != nil {
		return err
	}
	if a.flagErrorFormat != "text" && a.flagErrorFormat != "json" {
		return output.Usagef("--error-format must be text or json")
	}
	stdoutFile, _ := a.Stdout.(*os.File)
	stdinFile, _ := a.Stdin.(*os.File)
	stderrFile, _ := a.Stderr.(*os.File)
	a.Interactive = !a.flagNoInput && output.IsTerminal(stdinFile) && output.IsTerminal(stdoutFile) && output.IsTerminal(stderrFile)
	a.Printer = output.Printer{W: a.Stdout, Format: format, Color: output.ColorEnabled(a.flagNoColor, stdoutFile)}

	a.Paths = config.DefaultPaths()
	if a.Cfg, err = config.Load(a.Paths.ConfigFile); err != nil {
		return err
	}
	if a.Creds, err = config.LoadCredentials(a.Paths.CredentialsFile); err != nil {
		return err
	}
	a.Profile = config.ResolveProfile(a.flagProfile, a.Cfg)
	// Profile names end up in file paths and scheduler unit files.
	if !watchName.MatchString(a.Profile) {
		return output.Usagef("profile name %q: use letters, digits, dot, dash or underscore", a.Profile)
	}
	a.Country = config.ResolveCountry(a.flagCountry, a.Profile, a.Cfg, a.Creds)
	if len(a.Country) != 2 {
		return output.Usagef("country %q should be a two-letter code such as es, us or pl", a.Country)
	}

	a.Client = booksy.New(a.Country)
	a.Client.BaseURL = os.Getenv("BOOKSY_API_BASE")
	a.Client.Language = a.Cfg.Language
	if a.flagDebug {
		a.Client.Debug = a.Stderr
	}
	a.applyAPIKey()
	if err := a.applyProxy(); err != nil && !underCommand(cmd, "config") {
		return err
	}
	a.attachSession()
	return nil
}

// applyAPIKey picks the public web key: BOOKSY_API_KEY, then config, then
// one rediscovered earlier, then the built-in one. Whatever the source, a
// refusal triggers one rediscovery from booksy.com, saved for next time.
func (a *App) applyAPIKey() {
	a.keySource = "built-in"
	if raw, err := os.ReadFile(a.Paths.APIKeyFile); err == nil {
		if k := strings.TrimSpace(string(raw)); k != "" {
			a.Client.APIKey, a.keySource = k, "rediscovered ("+a.Paths.APIKeyFile+")"
		}
	}
	if a.Cfg.APIKey != "" {
		a.Client.APIKey, a.keySource = a.Cfg.APIKey, "config api_key"
	}
	if k := os.Getenv("BOOKSY_API_KEY"); k != "" {
		a.Client.APIKey, a.keySource = k, "BOOKSY_API_KEY"
	}
	a.Client.WebURL = os.Getenv("BOOKSY_WEB_BASE")
	a.Client.OnKeyRejected = func(ctx context.Context) (string, error) {
		a.notice("the web api key (%s) was refused; looking up the current one on booksy.com", a.keySource)
		k, err := a.Client.DiscoverAPIKey(ctx)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(a.Paths.APIKeyFile), 0o700); err == nil {
			_ = os.WriteFile(a.Paths.APIKeyFile, []byte(k+"\n"), 0o600)
		}
		if a.keySource == "config api_key" || a.keySource == "BOOKSY_API_KEY" {
			a.notice("found a new key and saved it; remove the stale %s so it is used", a.keySource)
		}
		a.keySource = "rediscovered (" + a.Paths.APIKeyFile + ")"
		return k, nil
	}
}

// applyProxy routes all Booksy traffic through BOOKSY_PROXY, else the config's
// proxy, with no fallback to a direct connection.
func (a *App) applyProxy() error {
	raw, src := os.Getenv("BOOKSY_PROXY"), "BOOKSY_PROXY"
	if raw == "" {
		raw, src = a.Cfg.Proxy, "config proxy"
	}
	if raw == "" {
		return nil
	}
	u, err := booksy.ParseProxy(raw)
	if err != nil {
		return output.Usagef("%s: %v. Nothing is sent to booksy until it is fixed", src, err)
	}
	if u.User != nil && src != "BOOKSY_PROXY" {
		return output.Usagef("config proxy: keep proxy credentials out of config.toml (it is printed by `config list` and copied into scheduler units). Pass them through BOOKSY_PROXY")
	}
	a.Client.UseProxy(u)
	return nil
}

func underCommand(cmd *cobra.Command, name string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == name && c.Parent() != nil && !c.Parent().HasParent() {
			return true
		}
	}
	return false
}

// attachSession wires the active profile's token (or BOOKSY_ACCESS_TOKEN)
// into the client. The fingerprint is always set: drafts need one even when
// anonymous.
func (a *App) attachSession() {
	s, ok := a.Creds.Profiles[a.Profile]
	if ok && s.Country == a.Country {
		a.Client.AccessToken = s.AccessToken
		a.Client.Fingerprint = s.Fingerprint
	}
	if tok := os.Getenv("BOOKSY_ACCESS_TOKEN"); tok != "" {
		a.Client.AccessToken = tok
		a.tokenFromEnv = true
	}
	if a.Client.Fingerprint == "" {
		a.Client.Fingerprint = a.Creds.Fingerprints[a.Profile]
	}
}

// ensureFingerprint makes sure the profile has a stable device id, minting
// and saving one the first time a draft is needed.
func (a *App) ensureFingerprint() error {
	if a.Client.Fingerprint != "" {
		return nil
	}
	fp := newUUID()
	a.Creds.Fingerprints[a.Profile] = fp
	a.Client.Fingerprint = fp
	if a.tokenFromEnv {
		return nil
	}
	return config.SaveCredentials(a.Paths.CredentialsFile, a.Creds)
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (a *App) requireSession() error {
	if a.Client.HasToken() {
		return nil
	}
	if s, ok := a.Creds.Profiles[a.Profile]; ok && s.Country != a.Country {
		return &booksy.Error{Kind: booksy.KindAuth, Msg: fmt.Sprintf("profile %q is logged in to booksy %s, not %s. Drop --country or log in again with --country %s", a.Profile, s.Country, a.Country, a.Country)}
	}
	return &booksy.Error{Kind: booksy.KindAuth, Msg: fmt.Sprintf("profile %q is not logged in. Run `booksy auth login`", a.Profile)}
}

func (a *App) Store() (*store.Store, error) {
	if a.store != nil {
		return a.store, nil
	}
	st, err := store.Open(a.Paths.StateDB)
	if err != nil {
		return nil, err
	}
	a.store = st
	return st, nil
}

// confirm asks a yes/no question when interactive. Non-interactive callers
// must pass --yes; otherwise this is a usage error, never a silent proceed.
func (a *App) confirm(yes bool, prompt string) error {
	if yes {
		return nil
	}
	if !a.Interactive {
		return output.Usagef("%s Pass --yes to confirm without a prompt", prompt)
	}
	fmt.Fprintf(a.Stderr, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(a.Stdin).ReadString('\n')
	if s := strings.ToLower(strings.TrimSpace(line)); s != "y" && s != "yes" && s != "s" && s != "si" && s != "sí" {
		return output.Usagef("cancelled")
	}
	return nil
}

func (a *App) notice(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "booksy: "+format+"\n", args...)
}

func (a *App) saveConfig() error { return config.Save(a.Paths.ConfigFile, a.Cfg) }

func (a *App) saveCreds() error { return config.SaveCredentials(a.Paths.CredentialsFile, a.Creds) }
