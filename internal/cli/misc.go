package cli

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/config"
	"github.com/GodGMN/booksy-cli/internal/output"
	"github.com/GodGMN/booksy-cli/internal/sink"
)

// config

// configTree is config.toml as nested maps keyed by the file's own names, so
// `config get/set` work on any key without a hand-written table.
func configTree(cfg config.Config) (map[string]any, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	tree := map[string]any{}
	return tree, json.Unmarshal(raw, &tree)
}

func treeToConfig(tree map[string]any) (config.Config, error) {
	raw, err := json.Marshal(tree)
	if err != nil {
		return config.Config{}, err
	}
	var cfg config.Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func lookup(tree map[string]any, key string) (any, bool) {
	var cur any = tree
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func assign(tree map[string]any, key string, value any) error {
	parts := strings.Split(key, ".")
	cur := tree
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			if _, exists := cur[p]; exists {
				return output.Usagef("%s is not a table", p)
			}
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	last := parts[len(parts)-1]
	if value == nil {
		delete(cur, last)
	} else {
		cur[last] = value
	}
	return nil
}

// parseValue reads numbers, booleans and JSON arrays as such; anything else
// is a string, so `config set country es` needs no quoting.
func parseValue(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		switch v.(type) {
		case float64, bool, []any:
			return v
		}
	}
	return s
}

func (a *App) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and change config.toml",
		Long: `config.toml keys (dotted paths):

  default_profile               profile used without --profile
  country                       fallback country code (es, us, pl, ...)
  language                      Accept-Language, e.g. es-ES or en-US
  proxy                         http(s)/socks5(h) URL, no credentials
  api_key                       override booksy.com's public web key (rarely needed)
  watch.interval                default watch interval, e.g. 15m
  profiles.NAME.country         a profile's country
  profiles.NAME.location.lat    default search centre (also .lng, .label)
  sinks.NAME.type               ntfy | webhook | exec (also .url .topic
                                .token_file .template .command)`,
	}
	cmd.AddCommand(&cobra.Command{
		Use: "path", Short: "Print the config and state file paths", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.Printer.Print(map[string]string{"config": a.Paths.ConfigFile, "credentials": a.Paths.CredentialsFile, "state": a.Paths.StateDB})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "Print the whole config", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.Printer.Print(a.Cfg) },
	})
	cmd.AddCommand(&cobra.Command{
		Use: "get <key>", Short: "Print one key", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tree, err := configTree(a.Cfg)
			if err != nil {
				return err
			}
			v, ok := lookup(tree, args[0])
			if !ok {
				return booksy.NotFound("%s is not set", args[0])
			}
			if s, ok := v.(string); ok {
				fmt.Fprintln(a.Stdout, s)
				return nil
			}
			return a.Printer.Print(v)
		},
	})
	set := func(use, short string, unset bool) *cobra.Command {
		n := 2
		if unset {
			n = 1
		}
		return &cobra.Command{
			Use: use, Short: short, Args: cobra.ExactArgs(n),
			RunE: func(cmd *cobra.Command, args []string) error {
				tree, err := configTree(a.Cfg)
				if err != nil {
					return err
				}
				var v any
				if !unset {
					v = parseValue(args[1])
					if strings.HasSuffix(args[0], ".command") {
						if s, ok := v.(string); ok {
							v = strings.Fields(s)
						}
					}
				}
				if err := assign(tree, args[0], v); err != nil {
					return err
				}
				cfg, err := treeToConfig(tree)
				if err != nil {
					return output.Usagef("%s: %v", args[0], err)
				}
				if cfg.Proxy != "" {
					if u, err := booksy.ParseProxy(cfg.Proxy); err != nil {
						return output.Usagef("proxy: %v", err)
					} else if u.User != nil {
						return output.Usagef("proxy: keep proxy credentials out of config.toml (it is printed by `config list`); pass them through BOOKSY_PROXY instead")
					}
				}
				if cfg.Watch.Interval != "" {
					if _, err := time.ParseDuration(cfg.Watch.Interval); err != nil {
						return output.Usagef("watch.interval: %v", err)
					}
				}
				a.Cfg = cfg
				return a.saveConfig()
			},
		}
	}
	cmd.AddCommand(set("set <key> <value>", "Set one key", false), set("unset <key>", "Remove one key", true))
	return cmd
}

// doctor

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, warn, fail, skip
	Detail string `json:"detail,omitempty"`
}

type doctorView []Check

func (v doctorView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, c := range v {
		st := c.Status
		if color {
			switch c.Status {
			case "ok":
				st = "\x1b[32mok\x1b[0m"
			case "warn":
				st = "\x1b[33mwarn\x1b[0m"
			case "fail":
				st = "\x1b[31mfail\x1b[0m"
			}
		}
		rows = append(rows, []string{st, c.Name, c.Detail})
	}
	output.Table(w, color, nil, rows)
}

func (a *App) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check config, network, login and state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			var checks []Check
			add := func(name, status, detail string) { checks = append(checks, Check{name, status, detail}) }

			add("config", "ok", a.Paths.ConfigFile)
			add("profile", "ok", fmt.Sprintf("%s (country %s)", a.Profile, a.Country))
			proxy := os.Getenv("BOOKSY_PROXY")
			if proxy == "" {
				proxy = a.Cfg.Proxy
			}
			if proxy != "" {
				u, _ := booksy.ParseProxy(proxy)
				if u != nil {
					u.User = nil
					proxy = u.String()
				}
				add("proxy", "ok", proxy)
			}
			start := time.Now()
			if cats, err := a.Client.Categories(ctx); err != nil {
				add("api", "fail", err.Error())
			} else {
				add("api", "ok", fmt.Sprintf("%s.booksy.com answered in %s (%d categories)", a.Country, time.Since(start).Round(time.Millisecond), len(cats)))
			}
			add("web api key", "ok", a.keySource)
			st := a.status()
			switch {
			case !st.LoggedIn:
				add("login", "skip", "not logged in; `booksy auth login` unlocks booking and appointments")
			default:
				if me, err := a.Client.Me(ctx); err != nil {
					add("login", "fail", err.Error())
				} else {
					detail := fmt.Sprintf("%s %s <%s>", me.FirstName, me.LastName, me.Email)
					if st.Warning != "" {
						add("login", "warn", detail+": "+st.Warning)
					} else {
						add("login", "ok", detail)
					}
				}
			}
			if s, err := a.Store(); err != nil {
				add("state", "fail", err.Error())
			} else if ws, err := s.Watches(ctx, ""); err != nil {
				add("state", "fail", err.Error())
			} else {
				add("state", "ok", fmt.Sprintf("%s (%d watches)", a.Paths.StateDB, len(ws)))
			}
			names := make([]string, 0, len(a.Cfg.Sinks))
			for n := range a.Cfg.Sinks {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				if err := sink.Validate(n, a.Cfg.Sinks[n]); err != nil {
					add("sink "+n, "fail", err.Error())
				} else {
					add("sink "+n, "ok", a.Cfg.Sinks[n].Type)
				}
			}
			if u := a.serviceUnit(); u != nil && u.installed() {
				state, _ := u.state()
				add("watch service", "ok", state)
			}
			if err := a.Printer.Print(doctorView(checks)); err != nil {
				return err
			}
			for _, c := range checks {
				if c.Status == "fail" {
					return &booksy.Error{Kind: booksy.KindGeneric, Msg: "doctor found problems: " + c.Name + ": " + c.Detail}
				}
			}
			return nil
		},
	}
}

// skills

//go:embed skills/*.md
var skillsFS embed.FS

func (a *App) skillsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skills", Short: "Embedded usage guides for agents and scripts"}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "Available guides", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, _ := fs.ReadDir(skillsFS, "skills")
			var names []string
			for _, e := range entries {
				names = append(names, strings.TrimSuffix(e.Name(), ".md"))
			}
			return a.Printer.Print(names)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "get <name>", Short: "Print a guide as markdown", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := skillsFS.ReadFile("skills/" + args[0] + ".md")
			if err != nil {
				return booksy.NotFound("no skill named %q (see `booksy skills list`)", args[0])
			}
			_, err = a.Stdout.Write(raw)
			return err
		},
	})
	return cmd
}
