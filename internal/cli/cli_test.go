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
		"BOOKSY_ACCESS_TOKEN": "", "BOOKSY_API_KEY": "", "BOOKSY_WEB_BASE": "", "BOOKSY_PROFILE": "", "BOOKSY_COUNTRY": "", "BOOKSY_CONFIG": "", "BOOKSY_PROXY": "",
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
	if h.srv.LastBookedStatus != "A" || res.Payment == nil || res.Payment.ToPayLater != "17.00" {
		t.Fatalf("booking must be priced and confirm: %s %+v", h.srv.LastBookedStatus, res.Payment)
	}
	h.srv.Prepayment = "5.00"
	if r := h.run("", "book", "3349", "--service", "273164", "--at", day+"T18:00", "--yes"); r.code != 2 || !strings.Contains(r.stderr, "prepayment") || h.srv.Scheduled != 1 {
		t.Fatalf("a prepayment must be refused before schedule: %d %s", r.code, r.stderr)
	}
	h.srv.Prepayment, h.srv.CancellationFee = "0.00", "11.00"
	if r := h.run("", "book", "3349", "--service", "273164", "--at", day+"T18:00", "--yes"); r.code != 2 || !strings.Contains(r.stderr, "no-show protection") || h.srv.Scheduled != 1 {
		t.Fatalf("a cancellation fee must be refused before schedule: %d %s", r.code, r.stderr)
	}
	dry := decode[BookingResult](t, h.ok("book", "3349", "--service", "273164", "--at", day+"T18:00", "--dry-run"))
	if dry.Payment == nil || dry.Payment.CancellationFee != "11.00" || h.srv.Scheduled != 1 {
		t.Fatalf("dry run should still show the fee: %+v", dry.Payment)
	}
	h.srv.CancellationFee = "0.00"
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

func TestWebKeyRotation(t *testing.T) {
	h := newHarness(t)
	t.Setenv("BOOKSY_WEB_BASE", h.srv.URL)
	h.srv.Key = "web-11111111-2222-3333-4444-555555555555"
	r := h.run("", "category", "list")
	if r.code != 0 || !strings.Contains(r.stderr, "web api key (built-in) was refused") {
		t.Fatalf("should recover: %d %s", r.code, r.stderr)
	}
	cached, err := os.ReadFile(filepath.Join(h.dir, "data", "booksy-cli", "web-api-key"))
	if err != nil || strings.TrimSpace(string(cached)) != h.srv.Key {
		t.Fatalf("key not cached: %q %v", cached, err)
	}
	hits := h.srv.WebHits
	r = h.run("", "category", "list")
	if r.code != 0 || h.srv.WebHits != hits || r.stderr != "" {
		t.Fatalf("the cached key should be used without rediscovery: %d hits %d->%d %s", r.code, hits, h.srv.WebHits, r.stderr)
	}
	checks := decode[[]Check](t, h.ok("doctor"))
	for _, c := range checks {
		if c.Name == "web api key" && !strings.HasPrefix(c.Detail, "rediscovered") {
			t.Fatalf("doctor: %+v", c)
		}
	}
	// booksy.com unreachable for discovery: exit 7, not 3.
	h.srv.Key = "web-99999999-2222-3333-4444-555555555555"
	t.Setenv("BOOKSY_WEB_BASE", "http://127.0.0.1:1")
	if r := h.run("", "category", "list"); r.code != 7 {
		t.Fatalf("unrecoverable key should exit 7: %d %s", r.code, r.stderr)
	}
}

func TestFailedDeliveryIsRetried(t *testing.T) {
	h := newHarness(t)
	h.srv.Slots[today(1)] = []string{"10:00"}
	flag := filepath.Join(h.dir, "fail")
	got := filepath.Join(h.dir, "got")
	script := filepath.Join(h.dir, "sink.sh")
	body := "#!/bin/sh\nif [ -e " + flag + " ]; then exit 1; fi\ncat >> " + got + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	h.ok("config", "set", "sinks.s.type", "exec")
	h.ok("config", "set", "sinks.s.command", script)
	h.ok("watch", "add", "slots", "3349", "--service", "273164", "--notify", "s", "--name", "w")

	os.WriteFile(flag, nil, 0o600)
	h.srv.Slots[today(1)] = []string{"10:00", "18:00"}
	if r := h.run("", "watch", "check", "w"); r.code == 0 || !strings.Contains(r.stderr, "sent again") {
		t.Fatalf("a failed delivery must fail the check: %d %s", r.code, r.stderr)
	}
	os.Remove(flag)
	evs := decode[[]any](t, h.ok("watch", "check", "w"))
	raw, _ := os.ReadFile(got)
	if len(evs) != 1 || !strings.Contains(string(raw), today(1)+"T18:00") {
		t.Fatalf("the slot should be delivered on the next check: %v %s", evs, raw)
	}
}

func TestWatchesStayInTheirProfile(t *testing.T) {
	h := newHarness(t)
	h.srv.Slots[today(1)] = []string{"10:00"}
	h.ok("watch", "add", "slots", "3349", "--service", "273164", "--name", "mine")
	for _, args := range [][]string{{"watch", "remove", "mine"}, {"watch", "check", "mine"}} {
		if r := h.run("", append(args, "--profile", "other")...); r.code != 4 || !strings.Contains(r.stderr, `profile "default"`) {
			t.Fatalf("%v from another profile: %d %s", args, r.code, r.stderr)
		}
	}
	h.ok("watch", "remove", "mine")
	for _, bad := range []string{"a b", "../x", "x\ny"} {
		if r := h.run("", "auth", "status", "--profile", bad); r.code != 2 {
			t.Fatalf("profile %q: %d", bad, r.code)
		}
	}
}

func TestSinkListHidesSecrets(t *testing.T) {
	h := newHarness(t)
	h.ok("config", "set", "sinks.d.type", "webhook")
	h.ok("config", "set", "sinks.d.url", "https://discord.com/api/webhooks/123/SECRET")
	h.ok("config", "set", "sinks.p.type", "ntfy")
	h.ok("config", "set", "sinks.p.url", "https://ntfy.sh")
	h.ok("config", "set", "sinks.p.topic", "verysecrettopic")
	out := h.ok("sink", "list")
	if strings.Contains(out, "SECRET") || strings.Contains(out, "verysecrettopic") || !strings.Contains(out, "discord.com") {
		t.Fatalf("%s", out)
	}
	info, _ := os.Stat(filepath.Join(h.dir, "config", "booksy-cli", "config.toml"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config.toml mode %v", info.Mode())
	}
}

func TestBookRefusalReasons(t *testing.T) {
	h := newHarness(t)
	day := today(1)
	h.srv.Slots[day] = []string{"18:00"}
	h.login()
	reason := func(args ...string) string {
		r := h.run("", append(args, "--error-format", "json")...)
		return decode[map[string]any](t, r.stderr)["reason"].(string)
	}
	if got := reason("book", "3349", "--service", "273164", "--at", day+"T09:00", "--yes"); got != "slot_unavailable" {
		t.Fatalf("%q", got)
	}
	h.srv.CancellationFee = "11.00"
	if got := reason("book", "3349", "--service", "273164", "--at", day+"T18:00", "--yes"); got != "card_required" || h.srv.Scheduled != 0 {
		t.Fatalf("%q scheduled=%d", got, h.srv.Scheduled)
	}
}
