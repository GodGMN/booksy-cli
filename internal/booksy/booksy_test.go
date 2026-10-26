package booksy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/fakebooksy"
)

func client(t *testing.T) (*booksy.Client, *fakebooksy.Server) {
	t.Helper()
	srv := fakebooksy.New()
	t.Cleanup(srv.Close)
	c := booksy.New("es")
	c.BaseURL = srv.URL
	return c, srv
}

func kind(err error) booksy.Kind {
	var e *booksy.Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return -1
}

func TestParseBusinessRef(t *testing.T) {
	for in, want := range map[string]int{
		"3349": 3349,
		"https://booksy.com/es-es/3349_barberia-cavalieri-estudios-12_barberia_53009_madrid":        3349,
		"https://booksy.com/es-es/3349_barberia-cavalieri-estudios-12_barberia_53009_madrid#ba_s=1": 3349,
		"3349_barberia-cavalieri-estudios-12_barberia_53009_madrid":                                 3349,
		"https://booksy.com/widget-2024/index.html?business=55555":                                  55555,
	} {
		got, err := booksy.ParseBusinessRef(in)
		if err != nil || got != want {
			t.Errorf("ParseBusinessRef(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := booksy.ParseBusinessRef("barberia"); kind(err) != booksy.KindUsage {
		t.Errorf("want usage error, got %v", err)
	}
}

func TestBusinessDecode(t *testing.T) {
	c, _ := client(t)
	b, err := c.Business(context.Background(), fakebooksy.BusinessID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Barberia Test" || b.Rating != 4.86 || b.URL != "https://booksy.com/es-es/3349_barberia-test_barberia_53009_madrid" {
		t.Errorf("summary: %+v", b.BusinessSummary)
	}
	if len(b.Staff) != 2 {
		t.Errorf("hidden staffer should be dropped: %+v", b.Staff)
	}
	if len(b.Services) != 3 {
		t.Errorf("inactive service should be dropped: %d services", len(b.Services))
	}
	if b.Services[0].Variants[0].PriceText != "17,00 €" {
		t.Errorf("nbsp not normalised: %q", b.Services[0].Variants[0].PriceText)
	}
	if b.OpenHours[1].Weekday != 7 {
		t.Errorf("sunday should be ISO 7, got %d", b.OpenHours[1].Weekday)
	}
	if _, err := c.Business(context.Background(), 1); kind(err) != booksy.KindNotFound {
		t.Errorf("want not found, got %v", err)
	}
}

func TestResolveVariant(t *testing.T) {
	c, _ := client(t)
	b, err := c.Business(context.Background(), fakebooksy.BusinessID)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ref  string
		want int
		kind booksy.Kind
	}{
		{"273164", 273164, 0},
		{"46629", 273164, 0},         // service id with one variant
		{"corte de pelo", 273164, 0}, // exact name beats the partial "corte y barba"
		{"SKIN", 273072, 0},
		{"corte y barba largo", 300002, 0},
		{"corte", 0, booksy.KindUsage}, // ambiguous
		{"46640", 0, booksy.KindUsage}, // service with two variants
		{"tinte", 0, booksy.KindNotFound},
		{"999", 0, booksy.KindNotFound},
		{"", 0, booksy.KindUsage},
	}
	for _, tc := range cases {
		_, v, err := b.ResolveVariant(tc.ref)
		if tc.kind != 0 {
			if kind(err) != tc.kind {
				t.Errorf("%q: want kind %v, got %v", tc.ref, tc.kind, err)
			}
			continue
		}
		if err != nil || v.ID != tc.want {
			t.Errorf("%q: got %d, %v; want %d", tc.ref, v.ID, err, tc.want)
		}
	}
}

func TestSlotFilter(t *testing.T) {
	slots := []booksy.Slot{
		{Date: "2026-10-26", Time: "09:00", Start: "2026-10-26T09:00"}, // Monday
		{Date: "2026-10-26", Time: "18:00", Start: "2026-10-26T18:00"},
		{Date: "2026-10-31", Time: "18:30", Start: "2026-10-31T18:30"}, // Saturday
		{Date: "2026-11-02", Time: "19:59", Start: "2026-11-02T19:59"},
		{Date: "2026-11-02", Time: "20:00", Start: "2026-11-02T20:00"},
	}
	starts := func(ss []booksy.Slot) string {
		var out []string
		for _, s := range ss {
			out = append(out, s.Start)
		}
		return strings.Join(out, " ")
	}
	if got := starts(booksy.SlotFilter{Between: "18:00-20:00"}.Apply(slots)); got != "2026-10-26T18:00 2026-10-31T18:30 2026-11-02T19:59" {
		t.Errorf("between: %s", got)
	}
	if got := starts(booksy.SlotFilter{Weekdays: []int{6}}.Apply(slots)); got != "2026-10-31T18:30" {
		t.Errorf("weekdays: %s", got)
	}
	if got := starts(booksy.SlotFilter{Before: "2026-10-31"}.Apply(slots)); got != "2026-10-26T09:00 2026-10-26T18:00" {
		t.Errorf("before date: %s", got)
	}
	if got := starts(booksy.SlotFilter{Before: "2026-10-31T19:00"}.Apply(slots)); got != "2026-10-26T09:00 2026-10-26T18:00 2026-10-31T18:30" {
		t.Errorf("before datetime: %s", got)
	}
	for _, bad := range []booksy.SlotFilter{{Between: "20:00-18:00"}, {Between: "18"}, {Weekdays: []int{8}}, {Before: "tomorrow"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v should not validate", bad)
		}
	}
}

func TestSlotsAndBooking(t *testing.T) {
	c, srv := client(t)
	ctx := context.Background()
	srv.Slots["2026-10-27"] = []string{"11:25", "18:00"}
	day, _ := time.Parse(time.DateOnly, "2026-10-27")
	slots, err := c.Slots(ctx, booksy.SlotsQuery{BusinessID: fakebooksy.BusinessID, VariantID: fakebooksy.VariantID, From: day, To: day})
	if err != nil || len(slots) != 2 || slots[1].Start != "2026-10-27T18:00" {
		t.Fatalf("slots: %+v %v", slots, err)
	}

	if _, err := c.CreateDraft(ctx, fakebooksy.BusinessID, fakebooksy.VariantID, -1); kind(err) != booksy.KindUsage {
		t.Fatalf("a draft without fingerprint should be refused locally, got %v", err)
	}
	c.Fingerprint = "fp"
	d, err := c.CreateDraft(ctx, fakebooksy.BusinessID, fakebooksy.VariantID, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ChangeTimeslot(ctx, d, "2026-10-27", "03:00"); kind(err) != booksy.KindUsage || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("closed slot: %v", err)
	}
	d, err = c.ChangeTimeslot(ctx, d, "2026-10-27", "18:00")
	if err != nil || d.From != "2026-10-27T18:00" || d.Version != "v2" {
		t.Fatalf("change: %+v %v", d, err)
	}
	sum, err := c.Confirmation(ctx, d)
	if err != nil || sum.Total != "17,00 €" || sum.Lines[0] != "Corte de pelo · 17,00 € · 11:25 - 11:50 · Empleado: Javier" {
		t.Fatalf("confirmation: %+v %v", sum, err)
	}
	if _, err := c.Schedule(ctx, d); kind(err) != booksy.KindAuth {
		t.Fatalf("schedule without token should be an auth error, got %v", err)
	}
	if srv.Scheduled != 0 {
		t.Fatal("schedule reached the server without a token")
	}
	c.AccessToken = fakebooksy.Token
	done, err := c.Schedule(ctx, d)
	if err != nil || done.CoreID != fakebooksy.AppointUID || srv.Scheduled != 1 {
		t.Fatalf("schedule: %+v %v", done, err)
	}
}

func TestErrorClassification(t *testing.T) {
	c, _ := client(t)
	ctx := context.Background()
	c.AccessToken = "wrong"
	_, err := c.Me(ctx)
	if kind(err) != booksy.KindAuth {
		t.Errorf("bad token: %v", err)
	}
	if _, err := c.ResolvePlace(ctx, "nowhere"); kind(err) != booksy.KindUsage {
		t.Errorf("unknown place: %v", err)
	}
	var e *booksy.Error
	_, err = c.Reviews(ctx, 1, 1, 1)
	if !errors.As(err, &e) || e.Kind != booksy.KindAPIChanged || strings.Contains(e.Endpoint, "/1/") {
		t.Errorf("405 should be api_changed with ids templated out: %+v", e)
	}
	c.BaseURL = "http://127.0.0.1:1"
	if _, err := c.Categories(ctx); kind(err) != booksy.KindNetwork {
		t.Errorf("unreachable: %v", err)
	}
}

func TestAppointments(t *testing.T) {
	c, srv := client(t)
	ctx := context.Background()
	if _, err := c.Appointments(ctx, 0); kind(err) != booksy.KindAuth {
		t.Fatalf("no token: %v", err)
	}
	c.AccessToken = fakebooksy.Token
	aps, err := c.Appointments(ctx, 0)
	if err != nil || len(aps) != 2 {
		t.Fatal(aps, err)
	}
	now, _ := time.Parse(time.RFC3339, "2026-10-26T12:00:00Z")
	if !aps[0].Upcoming(now) || aps[1].Upcoming(now) {
		t.Errorf("upcoming: %+v", aps)
	}
	if aps[0].Status != "confirmed" || aps[0].Staff[0] != "Javier" || aps[0].Total != "11,00 €" {
		t.Errorf("decode: %+v", aps[0])
	}
	if _, err := c.CancelAppointment(ctx, fakebooksy.AppointUID, 1); kind(err) != booksy.KindUsage {
		t.Errorf("stale version should be refused: %v", err)
	}
	ap, err := c.CancelAppointment(ctx, fakebooksy.AppointUID, fakebooksy.AppointVer)
	if err != nil || ap.Status != "cancelled" || srv.Cancelled != 1 {
		t.Errorf("cancel: %+v %v", ap, err)
	}
}

func TestRejectedKeyIsNotAnAuthError(t *testing.T) {
	c, srv := client(t)
	srv.Key = "web-11111111-2222-3333-4444-555555555555"
	_, err := c.Categories(context.Background())
	var e *booksy.Error
	if !errors.As(err, &e) || e.Kind != booksy.KindAPIChanged || !e.KeyRejected {
		t.Fatalf("a refused web key must be api_changed, not auth: %+v", err)
	}
}

func TestKeyRediscovery(t *testing.T) {
	c, srv := client(t)
	c.WebURL = srv.URL
	srv.Key = "web-11111111-2222-3333-4444-555555555555"
	got, err := c.DiscoverAPIKey(context.Background())
	if err != nil || got != srv.Key {
		t.Fatalf("discover: %q %v", got, err)
	}

	c.APIKey = booksy.WebAPIKey
	calls := 0
	c.OnKeyRejected = func(ctx context.Context) (string, error) {
		calls++
		return c.DiscoverAPIKey(ctx)
	}
	if _, err := c.Categories(context.Background()); err != nil || calls != 1 || c.APIKey != srv.Key {
		t.Fatalf("recover: %v calls=%d key=%s", err, calls, c.APIKey)
	}
	// The hook runs once per client; a second rotation fails plainly.
	srv.Key = "web-99999999-2222-3333-4444-555555555555"
	if _, err := c.Categories(context.Background()); kind(err) != booksy.KindAPIChanged || calls != 1 {
		t.Fatalf("second rotation: %v calls=%d", err, calls)
	}
}

func TestParseStartNeedsATime(t *testing.T) {
	for _, in := range []string{"2026-10-28", "2026-10-28 ", " 2026-10-28\n"} {
		if _, _, err := booksy.ParseStart(in); kind(err) != booksy.KindUsage {
			t.Errorf("%q should need a time, got %v", in, err)
		}
	}
	if d, c, err := booksy.ParseStart(" 2026-10-28 18:30 "); err != nil || d != "2026-10-28" || c != "18:30" {
		t.Errorf("%s %s %v", d, c, err)
	}
}

func TestRedirectsNeverLeaveTheOrigin(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Access-Token")
		w.Write([]byte(`{"customer":{"id":1}}`))
	}))
	defer other.Close()
	// 127.0.0.1 vs localhost: same machine, different origin.
	target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, http.StatusFound)
	}))
	defer api.Close()
	c := booksy.New("es")
	c.BaseURL, c.AccessToken = api.URL, "secret-token"
	if _, err := c.Me(context.Background()); err == nil {
		t.Fatal("a cross-origin redirect must fail")
	}
	if leaked != "" {
		t.Fatalf("token reached the redirect target: %q", leaked)
	}
}
