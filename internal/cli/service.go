package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/output"
)

// Scheduler integration, adapted from github.com/Microck/wallapop-cli (MIT).

func (a *App) watchServiceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Run checks on a schedule via systemd (Linux) or launchd (macOS)"}
	var interval time.Duration
	install := &cobra.Command{
		Use: "install", Short: "Install a user-level timer that runs `watch check --all`", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			iv, err := a.configuredInterval(interval)
			if err != nil {
				return err
			}
			return a.serviceInstall(iv)
		},
	}
	install.Flags().DurationVar(&interval, "interval", 0, "timer period (default watch.interval, else 15m)")
	cmd.AddCommand(install,
		&cobra.Command{Use: "uninstall", Short: "Remove the timer and its log", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return a.serviceUninstall() }},
		&cobra.Command{Use: "status", Short: "Show the timer's state and last run", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error { return a.serviceStatus() }},
	)
	return cmd
}

type serviceView struct {
	Platform string   `json:"platform"`
	Unit     string   `json:"unit,omitempty"`
	Files    []string `json:"files,omitempty"`
	Command  string   `json:"command,omitempty"`
	State    string   `json:"state,omitempty"`
	LastRun  string   `json:"last_run,omitempty"`
	Log      string   `json:"log,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

func (v serviceView) Pretty(w io.Writer, color bool) {
	rows := [][]string{{"platform", v.Platform}}
	if v.Unit != "" {
		rows = append(rows, []string{"unit", v.Unit})
	}
	for _, f := range v.Files {
		rows = append(rows, []string{"file", f})
	}
	if v.State != "" {
		rows = append(rows, []string{"state", v.State})
	}
	if v.LastRun != "" {
		rows = append(rows, []string{"last run", v.LastRun})
	}
	if v.Log != "" {
		rows = append(rows, []string{"log", v.Log})
	}
	if v.Command != "" {
		rows = append(rows, []string{"command", v.Command})
	}
	output.Table(w, color, nil, rows)
	if v.Hint != "" {
		fmt.Fprintln(w, v.Hint)
	}
}

func (a *App) serviceUnitName() string { return "booksy-watch-" + a.Profile }

// serviceLogFile is where the scheduler appends this profile's check output.
func (a *App) serviceLogFile() string {
	return filepath.Join(a.Paths.StateDir, a.serviceUnitName()+".log")
}

// serviceUnit is this profile's scheduler integration on the current platform.
// One GOOS switch decides platform, unit name and unit files; everything else
// reads from here. Nil on platforms without integration (Windows gets a
// printed command instead).
type serviceUnit struct {
	platform string   // systemd or launchd
	name     string   // timer unit or launchd label
	files    []string // written by install; any missing means not installed
}

func (a *App) serviceUnit() *serviceUnit {
	n := a.serviceUnitName()
	switch runtime.GOOS {
	case "linux":
		dir := filepath.Join(userHome(), ".config", "systemd", "user")
		return &serviceUnit{platform: "systemd", name: n + ".timer", files: []string{filepath.Join(dir, n+".service"), filepath.Join(dir, n+".timer")}}
	case "darwin":
		label := "com.github.godgmn.booksy-cli." + a.Profile
		return &serviceUnit{platform: "launchd", name: label, files: []string{filepath.Join(userHome(), "Library", "LaunchAgents", label+".plist")}}
	}
	return nil
}

func (u *serviceUnit) installed() bool {
	for _, f := range u.files {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	return true
}

// state asks the scheduler. "not installed" when the unit files are missing.
// On systemd an active timer that is not enabled dies at reboot, and a user
// manager without linger dies at logout; both are appended so "active" is
// never a false comfort. lastRun is systemd's last trigger time; launchd has
// no cheap equivalent.
func (u *serviceUnit) state() (state, lastRun string) {
	if !u.installed() {
		return "not installed", ""
	}
	if u.platform == "launchd" {
		if err := exec.Command("launchctl", "list", u.name).Run(); err != nil {
			return "not loaded", ""
		}
		return "loaded", ""
	}
	out, err := exec.Command("systemctl", "--user", "is-active", u.name).Output()
	state = strings.TrimSpace(string(out))
	if state == "" {
		if err != nil {
			return "unknown", ""
		}
		state = "inactive"
	}
	if enabled, _ := exec.Command("systemctl", "--user", "is-enabled", u.name).Output(); strings.TrimSpace(string(enabled)) != "enabled" {
		state += " (not enabled: gone after reboot)"
	}
	if !lingerEnabled() {
		state += " (linger off: stops at logout, run `loginctl enable-linger`)"
	}
	if last, err := exec.Command("systemctl", "--user", "show", "-p", "LastTriggerUSec", "--value", u.name).Output(); err == nil {
		if v := strings.TrimSpace(string(last)); v != "" && v != "n/a" {
			lastRun = v
		}
	}
	return state, lastRun
}

// lingerEnabled reports whether the user manager survives logout, which a
// user timer needs to run unattended and after a reboot. The user comes from
// the passwd database rather than $USER, which a scheduled or su'd process may
// not have: an empty name would make loginctl fail and every status line claim
// linger is off.
func lingerEnabled() bool {
	u, err := user.Current()
	if err != nil {
		return false
	}
	out, err := exec.Command("loginctl", "show-user", "--value", "-p", "Linger", u.Username).Output()
	return err == nil && strings.TrimSpace(string(out)) == "yes"
}

// serviceLogCap bounds the scheduler log: a month of five-minute checks that
// print nothing is a few hundred KB, so 1 MiB means rotation only happens on
// a busy or noisy watch.
const serviceLogCap = 1 << 20

// rotateServiceLog moves an oversized scheduler log to ".1" at the start of a
// check. systemd and launchd hold the old file open in append mode, so the
// current run keeps writing to the renamed file and the next run starts a
// fresh one. Rotation happens here rather than in the unit files because the
// two schedulers have no shared size-cap primitive and a shell wrapper in the
// units would be harder to test. No log file means nothing to do.
func (a *App) rotateServiceLog() {
	log := a.serviceLogFile()
	info, err := os.Stat(log)
	if err != nil || info.Size() <= serviceLogCap {
		return
	}
	if err := os.Rename(log, log+".1"); err != nil {
		fmt.Fprintf(a.Stderr, "booksy: could not rotate %s: %v\n", log, err)
	}
}

func (a *App) serviceCommandLine() (string, []string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	args := []string{"watch", "check", "--all", "--profile", a.Profile, "--format", "jsonl"}
	return exe, args, nil
}

// These are the variables that decide where the CLI reads and writes and
// which backend it talks to. systemd and launchd
// start a job with a bare environment, so anyone whose shell points the CLI
// elsewhere would get a timer reading a different, empty state database from
// the one they installed from, checking nothing and reporting nothing, with no
// error anywhere. HOME is in the list because it is where the paths come from
// when the XDG variables are unset, which is the common case.
// BOOKSY_ACCESS_TOKEN is deliberately absent: it is a secret, and a unit
// file is world-readable. BOOKSY_PROXY goes in, or the timer would talk to
// Booksy directly; install refuses one carrying credentials.
var (
	serviceEnvXDGKeys = []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"}
	serviceEnvURLKeys = []string{"BOOKSY_API_BASE", "BOOKSY_COUNTRY", "BOOKSY_PROXY"}
)

// serviceProxyCheck runs before a scheduled job is installed. BOOKSY_PROXY
// is copied into unit files, so it must not carry credentials. A Windows task
// carries no environment at all: it cannot get BOOKSY_PROXY, and it reads
// config.toml from the default location, so a config proxy only reaches it
// when no BOOKSY_CONFIG or XDG_CONFIG_HOME points elsewhere. Either way the
// job would connect directly, so install refuses. configProxy is the loaded
// config's proxy.
func serviceProxyCheck(windows bool, configProxy string) error {
	raw := os.Getenv("BOOKSY_PROXY")
	if windows {
		if raw != "" {
			return output.Usagef("a Windows scheduled task does not get BOOKSY_PROXY, so it would connect directly. Set the proxy with `booksy config set proxy URL` and unset BOOKSY_PROXY")
		}
		if configProxy != "" && (os.Getenv("BOOKSY_CONFIG") != "" || os.Getenv("XDG_CONFIG_HOME") != "") {
			return output.Usagef("a Windows scheduled task does not get BOOKSY_CONFIG or XDG_CONFIG_HOME, so it would read another config.toml and connect without the proxy. Keep the proxy in the default config.toml and unset them")
		}
		return nil
	}
	if raw == "" {
		return nil
	}
	u, err := booksy.ParseProxy(raw)
	if err != nil {
		return output.Usagef("BOOKSY_PROXY: %v", err)
	}
	if u.User != nil {
		return output.Usagef("BOOKSY_PROXY carries credentials, which would land in a unit file. Use a proxy without credentials (for example a local socks5h forwarder) for scheduled runs")
	}
	return nil
}

// serviceEnv freezes the current values of those variables into the unit, so
// the scheduled run resolves the same paths as the install did. Each group has
// its own rule, because each is read differently:
//
//   - HOME goes in as it is. Install refuses a relative one before this runs.
//   - An XDG variable goes in only when it is absolute. The spec calls a
//     relative one invalid and adrg/xdg ignores it, falling back to the HOME
//     default, so pinning it would turn an override the install ignored into
//     one the timer obeys: the empty-database bug again, from the other side.
//   - BOOKSY_CONFIG is read raw, so a relative one does take effect here and
//     must be resolved: the scheduler runs the job from its own directory.
func serviceEnv() [][2]string {
	var env [][2]string
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, [2]string{"HOME", home})
	}
	for _, k := range serviceEnvXDGKeys {
		if v := os.Getenv(k); filepath.IsAbs(v) {
			env = append(env, [2]string{k, v})
		}
	}
	if v := os.Getenv("BOOKSY_CONFIG"); v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			v = abs
		}
		env = append(env, [2]string{"BOOKSY_CONFIG", v})
	}
	for _, k := range serviceEnvURLKeys {
		if v := os.Getenv(k); v != "" {
			env = append(env, [2]string{k, v})
		}
	}
	return env
}

// systemdUnits renders the service and timer bodies for one profile.
//
// OnBootSec makes the first run happen shortly after boot (and right away on
// install, since boot is long past); OnUnitActiveSec keeps the cadence from
// each run's end. Persistent= is for OnCalendar timers only.
//
// ExecStart is word-split, so the executable is quoted: a home directory with
// a space in it would otherwise produce a unit that fails on every trigger.
// The append: paths take the rest of the line and need no quoting, but every
// value still has its % doubled against specifier expansion.
func systemdUnits(profile, exe string, args []string, logFile string, iv time.Duration, env [][2]string) (service, timer string) {
	var envLines strings.Builder
	for _, kv := range env {
		envLines.WriteString("Environment=" + systemdQuote(kv[0]+"="+kv[1]) + "\n")
	}
	log := systemdLiteral(logFile)
	service = fmt.Sprintf("[Unit]\nDescription=booksy-cli watch checks (%s)\n\n[Service]\nType=oneshot\n%sExecStart=%s %s\nStandardOutput=append:%s\nStandardError=append:%s\n",
		profile, envLines.String(), systemdQuote(exe), systemdLiteral(strings.Join(args, " ")), log, log)
	timer = fmt.Sprintf("[Unit]\nDescription=booksy-cli watch timer (%s)\n\n[Timer]\nOnBootSec=2m\nOnUnitActiveSec=%s\n\n[Install]\nWantedBy=timers.target\n",
		profile, formatSystemd(iv))
	return service, timer
}

// launchdPlist renders the agent. Every value is XML-escaped: a path or URL
// carrying & or < would otherwise produce a plist launchd refuses to parse,
// and an agent that fails to parse simply never runs. StartInterval counts
// whole seconds from each run; RunAtLoad makes the first one happen at load
// rather than an interval later, which is also how the agent comes back after
// a reboot, since launchd loads ~/Library/LaunchAgents at login.
func launchdPlist(label, exe string, args []string, logFile string, iv time.Duration, env [][2]string) string {
	var progArgs strings.Builder
	for _, s := range append([]string{exe}, args...) {
		progArgs.WriteString("    <string>" + xmlEscape(s) + "</string>\n")
	}
	var envDict strings.Builder
	if len(env) > 0 {
		envDict.WriteString("  <key>EnvironmentVariables</key><dict>\n")
		for _, kv := range env {
			envDict.WriteString("    <key>" + xmlEscape(kv[0]) + "</key><string>" + xmlEscape(kv[1]) + "</string>\n")
		}
		envDict.WriteString("  </dict>\n")
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array>
%s  </array>
%s  <key>StartInterval</key><integer>%d</integer>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlEscape(label), progArgs.String(), envDict.String(), int(iv.Seconds()), xmlEscape(logFile), xmlEscape(logFile))
}

// schtasksCreate is the command a Windows user runs by hand. schtasks counts
// whole minutes and rejects 0, so the interval rounds up to at least 1. The
// inner \" quotes keep a task command whose path has spaces in one argument,
// which "C:\Program Files\..." needs.
func schtasksCreate(taskName, exe string, args []string, iv time.Duration) string {
	return fmt.Sprintf(`schtasks /Create /SC MINUTE /MO %d /TN "%s" /TR "\"%s\" %s"`,
		max(1, int(math.Ceil(iv.Minutes()))), taskName, exe, strings.Join(args, " "))
}

func (a *App) serviceInstall(iv time.Duration) error {
	exe, args, err := a.serviceCommandLine()
	if err != nil {
		return err
	}
	u := a.serviceUnit()
	if err := serviceProxyCheck(runtime.GOOS == "windows", a.Cfg.Proxy); err != nil {
		return err
	}
	if u == nil {
		return a.printSchtasks(schtasksCreate(a.serviceUnitName(), exe, args, iv),
			"scheduled install is not automated on Windows; run the command above in cmd.exe")
	}
	logFile := a.serviceLogFile()
	// A scheduler shares no working directory with the install command, so
	// every path written into the unit has to be absolute already. These come
	// from $HOME, and a relative $HOME makes all of them relative: systemd
	// ignores a relative StandardOutput= and never looks where the units
	// landed. Refuse rather than install something that quietly does nothing.
	for _, p := range append([]string{logFile}, u.files...) {
		if !filepath.IsAbs(p) {
			return output.Usagef("%s is not an absolute path, so the scheduler could not use it. Set HOME to an absolute path and install again", p)
		}
	}
	if err := os.MkdirAll(a.Paths.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(u.files[0]), 0o755); err != nil {
		return err
	}
	view := serviceView{Platform: u.platform, Unit: u.name, Files: u.files, Log: logFile}
	switch u.platform {
	case "systemd":
		service, timer := systemdUnits(a.Profile, exe, args, logFile, iv, serviceEnv())
		if err := os.WriteFile(u.files[0], []byte(service), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(u.files[1], []byte(timer), 0o644); err != nil {
			return err
		}
		if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %v: %s", err, out)
		}
		if out, err := exec.Command("systemctl", "--user", "enable", "--now", u.name).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl enable: %v: %s", err, out)
		}
		// Without linger the user manager, and this timer with it, stops at
		// logout and does not start at boot until the next login.
		if !lingerEnabled() {
			if out, err := exec.Command("loginctl", "enable-linger").CombinedOutput(); err != nil {
				view.Hint = fmt.Sprintf("could not enable linger (%v: %s); run `loginctl enable-linger` so the timer survives logout and reboot", err, strings.TrimSpace(string(out)))
			}
		}
		view.State = "enabled"
	case "launchd":
		if err := os.WriteFile(u.files[0], []byte(launchdPlist(u.name, exe, args, logFile, iv, serviceEnv())), 0o644); err != nil {
			return err
		}
		_ = exec.Command("launchctl", "unload", u.files[0]).Run()
		if out, err := exec.Command("launchctl", "load", u.files[0]).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl load: %v: %s", err, out)
		}
		view.State = "loaded"
	}
	return a.Printer.Print(view)
}

// systemdLiteral escapes a value so systemd reads it as the text it is. A %
// starts a specifier expansion: an unknown one makes systemd drop the whole
// assignment, and a known one silently substitutes something else, so a path
// or a percent-encoded URL carrying % must double it.
func systemdLiteral(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}

// systemdQuote is systemdLiteral plus the double quotes the unit parser
// understands, so a path or a value with a space also survives word splitting.
func systemdQuote(s string) string {
	return `"` + systemdLiteral(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)) + `"`
}

// xmlEscape makes a string safe as plist text. encoding/xml's escaper turns
// newlines into entities, which is right but noisy; the five predefined
// entities are all a plist needs.
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

func formatSystemd(d time.Duration) string {
	if d%time.Minute == 0 {
		return strconv.Itoa(int(d.Minutes())) + "min"
	}
	return strconv.Itoa(int(d.Seconds())) + "s"
}

// printSchtasks is the Windows path: no integration, the user runs the printed
// command. Other platforms without a scheduler are refused outright rather
// than handed a cmd.exe command line.
func (a *App) printSchtasks(cmdline, hint string) error {
	if runtime.GOOS != "windows" {
		return output.Usagef("watch service has no scheduler integration on %s; use cron with `booksy watch check --all`", runtime.GOOS)
	}
	return a.Printer.Print(serviceView{Platform: "schtasks", Command: cmdline, Hint: hint})
}

// serviceUninstall stops the timer, then removes the units and the scheduler
// log, so nothing of the schedule outlives it. A scheduler that cannot be
// stopped is an error, not a "removed": deleting the files under a still
// loaded unit would leave it running blind. Watches and events stay in the
// state database.
func (a *App) serviceUninstall() error {
	u := a.serviceUnit()
	if u == nil {
		return a.printSchtasks(fmt.Sprintf(`schtasks /Delete /TN "%s" /F`, a.serviceUnitName()), "run the command above in cmd.exe")
	}
	if u.installed() {
		var stop *exec.Cmd
		switch u.platform {
		case "systemd":
			stop = exec.Command("systemctl", "--user", "disable", "--now", u.name)
		case "launchd":
			stop = exec.Command("launchctl", "unload", u.files[0])
		}
		if out, err := stop.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v: %s", strings.Join(stop.Args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	for _, f := range u.files {
		_ = os.Remove(f)
	}
	if u.platform == "systemd" {
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	log := a.serviceLogFile()
	_ = os.Remove(log)
	_ = os.Remove(log + ".1")
	return a.Printer.Print(serviceView{Platform: u.platform, Unit: u.name, State: "removed"})
}

func (a *App) serviceStatus() error {
	u := a.serviceUnit()
	if u == nil {
		return a.printSchtasks(fmt.Sprintf(`schtasks /Query /TN "%s"`, a.serviceUnitName()), "")
	}
	state, lastRun := u.state()
	return a.Printer.Print(serviceView{Platform: u.platform, Unit: u.name, State: state, LastRun: lastRun, Log: a.serviceLogFile()})
}

func userHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}
