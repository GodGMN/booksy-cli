package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/config"
	"github.com/GodGMN/booksy-cli/internal/cookies"
	"github.com/GodGMN/booksy-cli/internal/output"
)

// AuthStatus is the JSON contract of `auth status` and `auth login`.
type AuthStatus struct {
	Profile    string    `json:"profile"`
	Country    string    `json:"country"`
	LoggedIn   bool      `json:"logged_in"`
	Source     string    `json:"source,omitempty"`
	CustomerID int       `json:"customer_id,omitempty"`
	Name       string    `json:"name,omitempty"`
	Email      string    `json:"email,omitempty"`
	ImportedAt time.Time `json:"imported_at,omitzero"`
	ExpiresAt  time.Time `json:"expires_at,omitzero"`
	Valid      *bool     `json:"session_valid,omitempty"`
	Warning    string    `json:"warning,omitempty"`
}

func (s AuthStatus) Pretty(w io.Writer, color bool) {
	if !s.LoggedIn {
		fmt.Fprintf(w, "profile %s (%s): not logged in\n", s.Profile, s.Country)
		return
	}
	fmt.Fprintf(w, "profile %s (%s): %s <%s>\n", s.Profile, s.Country, s.Name, s.Email)
	if !s.ImportedAt.IsZero() {
		fmt.Fprintf(w, "imported %s\n", s.ImportedAt.Local().Format("2006-01-02 15:04"))
	}
	if !s.ExpiresAt.IsZero() {
		fmt.Fprintf(w, "expires  %s\n", s.ExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	if s.Valid != nil {
		fmt.Fprintf(w, "valid    %v\n", *s.Valid)
	}
	if s.Warning != "" {
		fmt.Fprintln(w, "warning: "+s.Warning)
	}
}

// expiryWarning flags a token that Booksy will drop within two weeks.
func expiryWarning(exp time.Time, now time.Time) string {
	if exp.IsZero() {
		return ""
	}
	if !exp.After(now) {
		return "the imported login has expired. Log in on booksy.com and run `booksy auth login` again"
	}
	if exp.Sub(now) < 14*24*time.Hour {
		return fmt.Sprintf("the imported login expires on %s. Log in on booksy.com and run `booksy auth login` again before then", exp.Local().Format("2006-01-02"))
	}
	return ""
}

func (a *App) status() AuthStatus {
	st := AuthStatus{Profile: a.Profile, Country: a.Country}
	if a.tokenFromEnv {
		st.LoggedIn, st.Source = true, "BOOKSY_ACCESS_TOKEN"
		return st
	}
	s, ok := a.Creds.Profiles[a.Profile]
	if !ok || s.Country != a.Country {
		return st
	}
	st.LoggedIn, st.Source = true, "credentials.toml"
	st.CustomerID, st.Name, st.Email, st.ImportedAt, st.ExpiresAt = s.CustomerID, s.Name, s.Email, s.ImportedAt, s.ExpiresAt
	st.Warning = expiryWarning(s.ExpiresAt, time.Now())
	return st
}

func (a *App) authCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Import, inspect or forget a booksy.com login"}

	var cookieFile, cookieFormat string
	var fromStdin bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Import your booksy.com login from browser cookies",
		Long: `booksy.com keeps your login in a cookie named identity-<country> (identity-es
in Spain). Log in on booksy.com in your browser, then give the CLI one of:

  - a cookie export: Cookie-Editor or EditThisCookie JSON, a Netscape
    cookies.txt, or a copied Cookie: header (--cookies FILE, or - for stdin)
  - the bare value of the identity-<country> cookie, pasted at the prompt

Only booksy.com cookies are read, so a whole-browser export is fine. Email,
Google, Apple and Facebook logins all end in the same cookie. The login is
checked against Booksy before it is saved to credentials.toml (mode 0600).`,
		Example: `  booksy auth login --cookies ~/Downloads/booksy-cookies.json
  pbpaste | booksy auth login --cookies -
  booksy auth login --country pl --profile poland`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.tokenFromEnv {
				return output.Usagef("BOOKSY_ACCESS_TOKEN is set, which overrides any saved login. Unset it to save one")
			}
			raw, err := a.readLoginInput(cookieFile, fromStdin)
			if err != nil {
				return err
			}
			cs, err := cookies.Parse(raw, cookies.Format(cookieFormat))
			if err != nil {
				return output.Usagef("%v", err)
			}
			ids := cookies.Identities(cs)
			id, err := a.pickIdentity(ids, cmd.Flags().Changed("country"))
			if err != nil {
				return err
			}
			fp := id.Fingerprint
			if fp == "" {
				fp = a.Creds.Profiles[a.Profile].Fingerprint
			}
			if fp == "" {
				fp = a.Creds.Fingerprints[a.Profile]
			}
			if fp == "" {
				fp = newUUID()
			}
			a.Country = id.Country
			c := booksy.New(id.Country)
			c.HTTP, c.BaseURL, c.Debug, c.Language = a.Client.HTTP, a.Client.BaseURL, a.Client.Debug, a.Client.Language
			c.AccessToken, c.Fingerprint = id.Token, fp
			me, err := c.Me(cmd.Context())
			if err != nil {
				if e, ok := err.(*booksy.Error); ok && e.Kind == booksy.KindAuth {
					e.Msg = "booksy did not accept that login. Log in on booksy.com again and export fresh cookies"
				}
				return err
			}
			a.Creds.Profiles[a.Profile] = config.Session{
				Country: id.Country, AccessToken: id.Token, Fingerprint: fp, CustomerID: me.ID,
				Name: strings.TrimSpace(me.FirstName + " " + me.LastName), Email: me.Email,
				ImportedAt: time.Now().UTC(), ExpiresAt: id.Expires,
			}
			delete(a.Creds.Fingerprints, a.Profile)
			if err := a.saveCreds(); err != nil {
				return err
			}
			if err := a.seedProfile(me, id.Country); err != nil {
				return err
			}
			a.Client.AccessToken = id.Token
			return a.Printer.Print(a.status())
		},
	}
	login.Flags().StringVar(&cookieFile, "cookies", "", "cookie export file, or - for stdin")
	login.Flags().BoolVar(&fromStdin, "cookies-stdin", false, "read the cookie export or token from stdin")
	login.Flags().StringVar(&cookieFormat, "cookies-format", "auto", "auto, json, netscape or header")

	var check bool
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the profile's login; --check asks Booksy if it still works",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st := a.status()
			if check && st.LoggedIn {
				_, err := a.Client.Me(cmd.Context())
				valid := err == nil
				if err != nil {
					if e, ok := err.(*booksy.Error); !ok || e.Kind != booksy.KindAuth {
						return err
					}
				}
				st.Valid = &valid
			}
			if st.Warning != "" && a.Printer.Format != output.Pretty {
				a.notice("warning: %s", st.Warning)
			}
			return a.Printer.Print(st)
		},
	}
	status.Flags().BoolVar(&check, "check", false, "verify the login against Booksy")

	var yes bool
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Forget the saved login (booksy.com stays logged in)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := a.Creds.Profiles[a.Profile]; !ok {
				return booksy.NotFound("profile %q has no saved login", a.Profile)
			}
			if err := a.confirm(yes, fmt.Sprintf("Forget the login for profile %q?", a.Profile)); err != nil {
				return err
			}
			delete(a.Creds.Profiles, a.Profile)
			if err := a.saveCreds(); err != nil {
				return err
			}
			a.Client.AccessToken = ""
			a.tokenFromEnv = false
			return a.Printer.Print(a.status())
		},
	}
	logout.Flags().BoolVar(&yes, "yes", false, "do not ask")

	cmd.AddCommand(login, status, logout)
	return cmd
}

func (a *App) readLoginInput(file string, fromStdin bool) (string, error) {
	switch {
	case file == "-" || fromStdin:
		raw, err := io.ReadAll(a.Stdin)
		return string(raw), err
	case file != "":
		raw, err := os.ReadFile(expandPath(file))
		if err != nil {
			return "", output.Usagef("cannot read %s: %v", file, err)
		}
		return string(raw), nil
	}
	if !a.Interactive {
		return "", output.Usagef("pass --cookies FILE (or - for stdin); there is no terminal to prompt on")
	}
	fmt.Fprintf(a.Stderr, "Log in on booksy.com, then paste the identity-%s cookie value or the path to a cookie export:\n> ", a.Country)
	line, err := bufio.NewReader(a.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", output.Usagef("no input")
	}
	line = strings.TrimSpace(line)
	if p := expandPath(line); fileExists(p) {
		raw, err := os.ReadFile(p)
		return string(raw), err
	}
	return line, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// pickIdentity chooses the cookie for the profile's country. With --country
// given it must match; otherwise a single identity cookie decides the country.
func (a *App) pickIdentity(ids []cookies.Identity, countryFlag bool) (cookies.Identity, error) {
	if len(ids) == 0 {
		return cookies.Identity{}, output.Usagef("no identity-<country> cookie found. Log in on booksy.com first, then export cookies for booksy.com")
	}
	for i := range ids {
		if ids[i].Country == "" {
			ids[i].Country = a.Country
		}
	}
	if len(ids) == 1 && !countryFlag {
		return ids[0], nil
	}
	for _, id := range ids {
		if id.Country == a.Country {
			return id, nil
		}
	}
	var have []string
	for _, id := range ids {
		have = append(have, id.Country)
	}
	return cookies.Identity{}, output.Usagef("the export has logins for %s but not %s. Pass --country with one of those", strings.Join(have, ", "), a.Country)
}

// seedProfile records the profile's country and, if it has none yet, the
// account's location as the default search centre. The first profile becomes
// the default.
func (a *App) seedProfile(me booksy.Me, country string) error {
	if a.Cfg.Profiles == nil {
		a.Cfg.Profiles = map[string]config.ProfileConfig{}
	}
	pc := a.Cfg.Profiles[a.Profile]
	pc.Country = country
	if pc.Location == nil && (me.Lat != 0 || me.Lng != 0) {
		pc.Location = &config.Location{Lat: me.Lat, Lng: me.Lng, Label: me.City}
	}
	a.Cfg.Profiles[a.Profile] = pc
	if a.Cfg.DefaultProfile == "" {
		a.Cfg.DefaultProfile = a.Profile
	}
	return a.saveConfig()
}

// profiles

type ProfileRow struct {
	Name     string `json:"name"`
	Default  bool   `json:"default"`
	Country  string `json:"country"`
	LoggedIn bool   `json:"logged_in"`
	Account  string `json:"account,omitempty"`
}

type profilesView []ProfileRow

func (v profilesView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, p := range v {
		mark := ""
		if p.Default {
			mark = "*"
		}
		in := "no"
		if p.LoggedIn {
			in = "yes"
		}
		rows = append(rows, []string{mark + p.Name, p.Country, in, p.Account})
	}
	output.Table(w, color, []string{"PROFILE", "COUNTRY", "LOGGED IN", "ACCOUNT"}, rows)
}

func (a *App) profileCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Several accounts or countries side by side"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Known profiles; * marks the default",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			def := a.Cfg.DefaultProfile
			if def == "" {
				def = "default"
			}
			out := profilesView{}
			for _, n := range config.ProfileNames(a.Cfg, a.Creds) {
				row := ProfileRow{Name: n, Default: n == def, Country: config.ResolveCountry("", n, a.Cfg, a.Creds)}
				if s, ok := a.Creds.Profiles[n]; ok {
					row.LoggedIn, row.Account = true, s.Email
				}
				out = append(out, row)
			}
			return a.Printer.Print(out)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "use <name>",
		Short: "Make a profile the default",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.Cfg.DefaultProfile = args[0]
			if err := a.saveConfig(); err != nil {
				return err
			}
			fmt.Fprintf(a.Stderr, "default profile is now %s\n", args[0])
			return nil
		},
	})
	var yes bool
	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Forget a profile, its login and its watches",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			_, inCfg := a.Cfg.Profiles[name]
			_, inCreds := a.Creds.Profiles[name]
			if !inCfg && !inCreds {
				return booksy.NotFound("no profile named %q", name)
			}
			if err := a.confirm(yes, fmt.Sprintf("Remove profile %q with its login and watches?", name)); err != nil {
				return err
			}
			st, err := a.Store()
			if err != nil {
				return err
			}
			if err := st.RemoveProfileWatches(cmd.Context(), name); err != nil {
				return err
			}
			delete(a.Creds.Profiles, name)
			delete(a.Creds.Fingerprints, name)
			if err := a.saveCreds(); err != nil {
				return err
			}
			delete(a.Cfg.Profiles, name)
			if a.Cfg.DefaultProfile == name {
				a.Cfg.DefaultProfile = ""
			}
			return a.saveConfig()
		},
	}
	remove.Flags().BoolVar(&yes, "yes", false, "do not ask")
	cmd.AddCommand(remove)
	return cmd
}
