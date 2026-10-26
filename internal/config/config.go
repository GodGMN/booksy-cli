// Package config owns everything the CLI persists outside the state database:
// XDG paths, config.toml (hand-edited, world-readable) and credentials.toml
// (access tokens, 0600). The two files are kept apart so that sharing or
// committing a config never leaks a token.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

const appDir = "booksy-cli"

// DefaultCountry is used when neither the profile, the config nor
// BOOKSY_COUNTRY names one.
const DefaultCountry = "es"

// Paths resolves where each file lives. BOOKSY_CONFIG moves only config.toml;
// credentials and state follow XDG so a relocated config cannot point the CLI
// at someone else's tokens.
type Paths struct {
	ConfigFile      string
	CredentialsFile string
	StateDB         string
	StateDir        string
	// APIKeyFile caches a web api key rediscovered from booksy.com.
	APIKeyFile string
}

func DefaultPaths() Paths {
	xdg.Reload()
	p := Paths{
		ConfigFile:      filepath.Join(xdg.ConfigHome, appDir, "config.toml"),
		CredentialsFile: filepath.Join(xdg.ConfigHome, appDir, "credentials.toml"),
		StateDB:         filepath.Join(xdg.DataHome, appDir, "state.db"),
		StateDir:        filepath.Join(xdg.StateHome, appDir),
		APIKeyFile:      filepath.Join(xdg.DataHome, appDir, "web-api-key"),
	}
	if v := os.Getenv("BOOKSY_CONFIG"); v != "" {
		p.ConfigFile = v
	}
	return p
}

// Location is a default search centre.
type Location struct {
	Lat   float64 `toml:"lat" json:"lat"`
	Lng   float64 `toml:"lng" json:"lng"`
	Label string  `toml:"label,omitempty" json:"label,omitempty"`
}

type ProfileConfig struct {
	Country  string    `toml:"country,omitempty" json:"country,omitempty"`
	Location *Location `toml:"location,omitempty" json:"location,omitempty"`
}

// SinkConfig is one notification destination. Type selects which fields matter:
// ntfy uses URL+Topic(+TokenFile), webhook uses URL(+Template), exec uses Command.
type SinkConfig struct {
	Type      string   `toml:"type" json:"type"`
	URL       string   `toml:"url,omitempty" json:"url,omitempty"`
	Topic     string   `toml:"topic,omitempty" json:"topic,omitempty"`
	TokenFile string   `toml:"token_file,omitempty" json:"token_file,omitempty"`
	Template  string   `toml:"template,omitempty" json:"template,omitempty"`
	Command   []string `toml:"command,omitempty" json:"command,omitempty"`
}

type WatchConfig struct {
	Interval string `toml:"interval,omitempty" json:"interval,omitempty"`
}

type Config struct {
	DefaultProfile string `toml:"default_profile,omitempty" json:"default_profile,omitempty"`
	// Country is the fallback for profiles that do not set their own.
	Country string `toml:"country,omitempty" json:"country,omitempty"`
	// Language goes out as Accept-Language (es-ES, en-US, ...). Booksy uses it
	// for service names, error text and confirmation summaries.
	Language string `toml:"language,omitempty" json:"language,omitempty"`
	// Proxy routes all Booksy traffic through http(s)/socks5(h) and never falls
	// back to a direct connection. BOOKSY_PROXY overrides it. No credentials
	// here: this file is world-readable.
	Proxy string `toml:"proxy,omitempty" json:"proxy,omitempty"`
	// APIKey overrides booksy.com's public web key. Normally unset: the CLI
	// ships the current key and rediscovers it if Booksy changes it.
	APIKey   string                   `toml:"api_key,omitempty" json:"api_key,omitempty"`
	Profiles map[string]ProfileConfig `toml:"profiles,omitempty" json:"profiles,omitempty"`
	Watch    WatchConfig              `toml:"watch,omitempty" json:"watch,omitempty"`
	Sinks    map[string]SinkConfig    `toml:"sinks,omitempty" json:"sinks,omitempty"`
}

// Load reads config.toml. A missing file is an empty config, not an error.
// Unknown keys are errors so typos surface instead of silently doing nothing.
func Load(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	dec := toml.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	out, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeAtomic(path, out, 0o644)
}

// Session is one profile's imported login. The access token is the value of
// booksy.com's identity-{country} cookie; it is long-lived (about six months)
// and Booksy does not rotate it, so there is nothing to renew.
type Session struct {
	Country     string    `toml:"country" json:"country"`
	AccessToken string    `toml:"access_token" json:"-"`
	Fingerprint string    `toml:"fingerprint" json:"-"`
	CustomerID  int       `toml:"customer_id" json:"customer_id"`
	Name        string    `toml:"name" json:"name"`
	Email       string    `toml:"email" json:"email"`
	ImportedAt  time.Time `toml:"imported_at" json:"imported_at"`
	// ExpiresAt is the cookie's own expiry when the import carried one.
	ExpiresAt time.Time `toml:"expires_at,omitempty" json:"expires_at,omitempty"`
}

type Credentials struct {
	Profiles map[string]Session `toml:"profiles"`
	// Fingerprints holds a device id per profile even before login, so
	// anonymous booking drafts look like one stable browser.
	Fingerprints map[string]string `toml:"fingerprints,omitempty"`
}

func LoadCredentials(path string) (Credentials, error) {
	c := Credentials{Profiles: map[string]Session{}, Fingerprints: map[string]string{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := toml.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Session{}
	}
	if c.Fingerprints == nil {
		c.Fingerprints = map[string]string{}
	}
	return c, nil
}

func SaveCredentials(path string, c Credentials) error {
	out, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return writeAtomic(path, out, 0o600)
}

// ResolveProfile: flag, then BOOKSY_PROFILE, then config default, then "default".
func ResolveProfile(flag string, cfg Config) string {
	if flag != "" {
		return flag
	}
	if v := os.Getenv("BOOKSY_PROFILE"); v != "" {
		return v
	}
	if cfg.DefaultProfile != "" {
		return cfg.DefaultProfile
	}
	return "default"
}

// ResolveCountry: flag, BOOKSY_COUNTRY, the profile's session, the profile's
// config, the global config, then DefaultCountry.
func ResolveCountry(flag, profile string, cfg Config, creds Credentials) string {
	for _, c := range []string{flag, os.Getenv("BOOKSY_COUNTRY"), creds.Profiles[profile].Country, cfg.Profiles[profile].Country, cfg.Country} {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			return c
		}
	}
	return DefaultCountry
}

// ProfileNames lists every profile known to either file, sorted.
func ProfileNames(cfg Config, creds Credentials) []string {
	seen := map[string]bool{}
	for n := range cfg.Profiles {
		seen[n] = true
	}
	for n := range creds.Profiles {
		seen[n] = true
	}
	var out []string
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
