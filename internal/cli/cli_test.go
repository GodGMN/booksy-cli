package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GodGMN/booksy-cli/internal/fakebooksy"
)

type harness struct {
	t   *testing.T
	srv *fakebooksy.Server
	dir string
}

type result struct {
	code           int
	stdout, stderr string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv := fakebooksy.New()
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	for k, v := range map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "XDG_DATA_HOME": filepath.Join(dir, "data"),
		"XDG_STATE_HOME": filepath.Join(dir, "state"), "HOME": dir, "BOOKSY_API_BASE": srv.URL,
		"BOOKSY_ACCESS_TOKEN": "", "BOOKSY_PROFILE": "", "BOOKSY_COUNTRY": "", "BOOKSY_CONFIG": "", "BOOKSY_PROXY": "",
	} {
		t.Setenv(k, v)
	}
	return &harness{t: t, srv: srv, dir: dir}
}

func (h *harness) run(stdin string, args ...string) result {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := Execute("test", args, strings.NewReader(stdin), &out, &errb)
	return result{code, out.String(), errb.String()}
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()
	r := h.run("", args...)
	if r.code != 0 {
		h.t.Fatalf("booksy %s: exit %d\nstderr: %s", strings.Join(args, " "), r.code, r.stderr)
	}
	return r.stdout
}

func (h *harness) login() {
	h.t.Helper()
	path := filepath.Join(h.dir, "cookies.json")
	raw := `[{"domain":"booksy.com","name":"identity-es","value":"` + fakebooksy.Token + `","expirationDate":1808595181},{"domain":"booksy.com","name":"_bf","value":"fp-test"}]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.ok("auth", "login", "--cookies", path)
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

func today(offset int) string { return time.Now().AddDate(0, 0, offset).Format(time.DateOnly) }

func TestSearchAndBusiness(t *testing.T) {
	h := newHarness(t)
	page := decode[map[string]any](t, h.ok("search", "barberia", "--near", "Valencia"))
	if page["total"].(float64) != 246 {
		t.Fatalf("%v", page)
	}
	if !strings.Contains(strings.Join(h.srv.CallLog(), "\n"), "GET /search/street_hints/resolve/") {
		t.Fatal("--near should resolve the place")
	}
	lines := strings.Split(strings.TrimSpace(h.ok("search", "--format", "jsonl")), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], `{"id":3349`) {
		t.Fatalf("jsonl: %v", lines)
	}
	pretty := h.ok("business", "show", "https://booksy.com/es-es/3349_barberia-test_barberia_53009_madrid", "--format", "pretty")
	for _, want := range []string{"Barberia Test", "Corte de pelo", "273164", "Sun", "staff: Javier, Roy"} {
		if !strings.Contains(pretty, want) {
			t.Errorf("business show pretty lacks %q:\n%s", want, pretty)
		}
	}
	if r := h.run("", "business", "show", "1"); r.code != 4 {
		t.Fatalf("missing business should exit 4, got %d: %s", r.code, r.stderr)
	}
	if r := h.run("", "business", "show", "nope"); r.code != 2 {
		t.Fatalf("bad ref should exit 2, got %d", r.code)
	}
}

func TestSlots(t *testing.T) {
	h := newHarness(t)
	h.srv.Slots[today(1)] = []string{"10:00", "17:30", "19:00"}
	res := decode[SlotsResult](t, h.ok("slots", "3349", "--service", "corte de pelo", "--between", "17:00-20:00", "--staff", "jav"))
	if res.VariantID != fakebooksy.VariantID || res.StafferID != fakebooksy.StafferID || len(res.Slots) != 2 || res.Slots[0].Time != "17:30" {
		t.Fatalf("%+v", res)
	}
	first := decode[SlotsResult](t, h.ok("slots", "3349", "--service", "273164", "--first"))
	if len(first.Slots) != 1 || first.Slots[0].Time != "10:00" {
		t.Fatalf("--first: %+v", first)
	}
	r := h.run("", "slots", "3349", "--service", "corte", "--error-format", "json")
	env := decode[map[string]any](t, r.stderr)
	if r.code != 2 || env["category"] != "usage" || !strings.Contains(env["message"].(string), "273164") {
		t.Fatalf("ambiguous service: %d %v", r.code, env)
	}
	if r := h.run("", "slots", "3349", "--service", "273164", "--weekdays", "funday"); r.code != 2 {
		t.Fatalf("bad weekday: %d", r.code)
	}
}

func TestBookFlow(t *testing.T) {
	h := newHarness(t)
	day := today(1)
	h.srv.Slots[day] = []string{"18:00"}

	// Dry run works without a login and never schedules.
	res := decode[BookingResult](t, h.ok("book", "3349", "--service", "273164", "--at", day+"T18:00", "--dry-run"))
	if !res.DryRun || res.Booked || res.Summary.Total != "17,00 €" || h.srv.Scheduled != 0 {
		t.Fatalf("dry run: %+v", res)
	}
	// The fingerprint minted for the anonymous draft is kept for the profile.
	creds, _ := os.ReadFile(filepath.Join(h.dir, "config", "booksy-cli", "credentials.toml"))
	if !strings.Contains(string(creds), "[fingerprints]") {
		t.Fatalf("fingerprint not persisted:\n%s", creds)
	}

	if r := h.run("", "book", "3349", "--service", "273164", "--at", day+"T18:00"); r.code != 3 {
		t.Fatalf("booking without login should exit 3, got %d: %s", r.code, r.stderr)
	}
	h.login()
	if r := h.run("", "book", "3349", "--service", "273164", "--at", day+"T18:00"); r.code != 2 || !strings.Contains(r.stderr, "--yes") || h.srv.Scheduled != 0 {
		t.Fatalf("non-interactive booking without --yes must refuse: %d %s", r.code, r.stderr)
	}
	if r := h.run("", "book", "3349", "--service", "273164", "--at", day+"T09:00", "--yes"); r.code != 2 || !strings.Contains(r.stderr, "not available") {
		t.Fatalf("closed slot: %d %s", r.code, r.stderr)
	}
	res = decode[BookingResult](t, h.ok("book", "3349", "--service", "273164", "--staff", "Javier", "--first", "--yes", "--note", "hola"))
	if !res.Booked || res.Appointment == nil || res.Appointment.UID != fakebooksy.AppointUID || h.srv.Scheduled != 1 {
		t.Fatalf("book: %+v", res)
	}
	calls := strings.Join(h.srv.CallLog(), "\n")
	for _, want := range []string{"change_staffer", "add_customer_note", "/schedule"} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing call %s in:\n%s", want, calls)
		}
	}
	if r := h.run("", "book", "3349", "--service", "273164"); r.code != 2 {
		t.Fatalf("neither --at nor --first: %d", r.code)
	}
}

func TestAuthAndAppointments(t *testing.T) {
	h := newHarness(t)
	st := decode[AuthStatus](t, h.ok("auth", "status"))
	if st.LoggedIn {
		t.Fatal("fresh profile should be logged out")
	}
	if r := h.run("", "appointment", "list"); r.code != 3 {
		t.Fatalf("appointments without login: %d", r.code)
	}
	if r := h.run("not-a-token!", "auth", "login", "--cookies", "-"); r.code != 2 {
		t.Fatalf("garbage login: %d %s", r.code, r.stderr)
	}
	if r := h.run("identity-es=wrongtoken0123456789abcdefghijkl", "auth", "login", "--cookies", "-"); r.code != 3 {
		t.Fatalf("rejected token should exit 3: %d %s", r.code, r.stderr)
	}
	h.login()
	st = decode[AuthStatus](t, h.ok("auth", "status", "--check"))
	if !st.LoggedIn || st.Valid == nil || !*st.Valid || st.Email != "test@example.com" || st.ExpiresAt.IsZero() {
		t.Fatalf("%+v", st)
	}
	info, err := os.Stat(filepath.Join(h.dir, "config", "booksy-cli", "credentials.toml"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode: %v %v", info.Mode(), err)
	}
	cfg, _ := os.ReadFile(filepath.Join(h.dir, "config", "booksy-cli", "config.toml"))
	if strings.Contains(string(cfg), fakebooksy.Token) || !strings.Contains(string(cfg), "lat = 39.47") {
		t.Fatalf("config.toml must hold the seeded location and never the token:\n%s", cfg)
	}

	up := decode[[]map[string]any](t, h.ok("appointment", "list", "--upcoming"))
	if len(up) != 1 || up[0]["uid"].(float64) != fakebooksy.AppointUID {
		t.Fatalf("upcoming: %v", up)
	}
	if r := h.run("", "appointment", "cancel", "123456789"); r.code != 2 || h.srv.Cancelled != 0 {
		t.Fatalf("cancel without --yes must refuse: %d", r.code)
	}
	ap := decode[map[string]any](t, h.ok("appointment", "cancel", "123456789", "--yes"))
	if ap["status"] != "cancelled" || h.srv.Cancelled != 1 {
		t.Fatalf("cancel: %v", ap)
	}
	if r := h.run("", "appointment", "cancel", "123456789", "--yes"); r.code != 2 {
		t.Fatalf("cancelling a cancelled appointment: %d", r.code)
	}

	// BOOKSY_ACCESS_TOKEN overrides and is never saved.
	h.ok("auth", "logout", "--yes")
	t.Setenv("BOOKSY_ACCESS_TOKEN", fakebooksy.Token)
	if st := decode[AuthStatus](t, h.ok("auth", "status")); st.Source != "BOOKSY_ACCESS_TOKEN" {
		t.Fatalf("%+v", st)
	}
	h.ok("me", "show")
}

func TestWatch(t *testing.T) {
	h := newHarness(t)
	h.srv.Slots[today(1)] = []string{"10:00"}
	sinkOut := filepath.Join(h.dir, "event.json")
	script := filepath.Join(h.dir, "sink.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > "+sinkOut+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.ok("config", "set", "sinks.local.type", "exec")
	h.ok("config", "set", "sinks.local.command", script)
	if r := h.run("", "watch", "add", "slots", "3349", "--service", "273164", "--notify", "nope"); r.code != 2 {
		t.Fatalf("unknown sink: %d", r.code)
	}
	w := decode[map[string]any](t, h.ok("watch", "add", "slots", "3349", "--service", "273164", "--notify", "local", "--name", "barber"))
	if w["name"] != "barber" || w["interval"] != "15m0s" {
		t.Fatalf("%v", w)
	}
	if evs := decode[[]any](t, h.ok("watch", "check", "barber")); len(evs) != 0 {
		t.Fatalf("nothing changed, got %v", evs)
	}
	// Someone cancels: a new slot inside the window opens.
	h.srv.Slots[today(1)] = []string{"10:00", "18:00"}
	evs := decode[[]map[string]any](t, h.ok("watch", "check", "barber"))
	if len(evs) != 1 || evs[0]["type"] != "slots.opened" {
		t.Fatalf("%v", evs)
	}
	raw, err := os.ReadFile(sinkOut)
	if err != nil || !strings.Contains(string(raw), today(1)+"T18:00") {
		t.Fatalf("sink did not get the event: %s %v", raw, err)
	}
	if evs := decode[[]any](t, h.ok("watch", "check", "barber")); len(evs) != 0 {
		t.Fatalf("second check should be quiet: %v", evs)
	}
	if hist := decode[[]any](t, h.ok("watch", "events")); len(hist) != 1 {
		t.Fatalf("history: %v", hist)
	}
	// Not due yet without a name or --all.
	if evs := decode[[]any](t, h.ok("watch", "check")); len(evs) != 0 {
		t.Fatalf("%v", evs)
	}
	h.ok("watch", "remove", "barber")
	if r := h.run("", "watch", "remove", "barber"); r.code != 4 {
		t.Fatalf("remove twice: %d", r.code)
	}
}

func TestConfigAndDoctor(t *testing.T) {
	h := newHarness(t)
	h.ok("config", "set", "country", "pl")
	if got := strings.TrimSpace(h.ok("config", "get", "country")); got != "pl" {
		t.Fatalf("get: %q", got)
	}
	if r := h.run("", "config", "set", "nonsense.key", "1"); r.code != 2 {
		t.Fatalf("unknown key: %d %s", r.code, r.stderr)
	}
	if r := h.run("", "config", "set", "proxy", "http://user:pw@host:1"); r.code != 2 {
		t.Fatalf("proxy with credentials: %d", r.code)
	}
	h.ok("config", "unset", "country")
	out := decode[[]Check](t, h.ok("doctor"))
	byName := map[string]string{}
	for _, c := range out {
		byName[c.Name] = c.Status
	}
	if byName["api"] != "ok" || byName["login"] != "skip" || byName["state"] != "ok" {
		t.Fatalf("%v", out)
	}
	if !strings.Contains(h.ok("skills", "get", "booksy-usage"), "--dry-run") {
		t.Fatal("skill text")
	}
}
