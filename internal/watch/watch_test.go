package watch

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/store"
)

func slot(start string) booksy.Slot {
	return booksy.Slot{Date: start[:10], Time: start[11:], Start: start}
}

func seenOf(keys ...string) map[string]store.Seen {
	m := map[string]store.Seen{}
	for _, k := range keys {
		m[k] = store.Seen{Key: k}
	}
	return m
}

var (
	now    = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	target = SlotsTarget{BusinessID: 1, Business: "B", Service: "S", VariantID: 2, Days: 7}
)

func opened(t *testing.T, r Result) []string {
	t.Helper()
	if len(r.Events) == 0 {
		return nil
	}
	if len(r.Events) != 1 || r.Events[0].Type != EventSlotsOpened {
		t.Fatalf("events: %+v", r.Events)
	}
	var o Opened
	if err := json.Unmarshal(r.Events[0].Data, &o); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range o.Slots {
		out = append(out, s.Start)
	}
	if o.Earliest.Start != out[0] {
		t.Fatalf("earliest %s, slots %v", o.Earliest.Start, out)
	}
	return out
}

func TestBaselineIsSilent(t *testing.T) {
	w := store.Watch{Name: "w"}
	r := Diff(w, target, nil, []booksy.Slot{slot("2026-10-27T10:00")}, now, false)
	if len(r.Events) != 0 || len(r.Present) != 1 {
		t.Fatalf("%+v", r)
	}
	r = Diff(w, target, nil, []booksy.Slot{slot("2026-10-27T10:00")}, now, true)
	if got := opened(t, r); len(got) != 1 {
		t.Fatalf("emit initial: %v", got)
	}
}

func TestCancellationFiresOnce(t *testing.T) {
	w := store.Watch{Name: "w", Baselined: true, LastCheckAt: now.Add(-15 * time.Minute)}
	seen := seenOf("2026-10-27T10:00", "2026-11-01T10:00")
	open := []booksy.Slot{slot("2026-10-27T10:00"), slot("2026-10-28T18:00"), slot("2026-10-27T17:00"), slot("2026-11-01T10:00")}
	got := opened(t, Diff(w, target, seen, open, now, false))
	if len(got) != 2 || got[0] != "2026-10-27T17:00" || got[1] != "2026-10-28T18:00" {
		t.Fatalf("want the two new slots sorted, got %v", got)
	}
	seen = seenOf("2026-10-27T10:00", "2026-10-28T18:00", "2026-10-27T17:00", "2026-11-01T10:00")
	if got := opened(t, Diff(w, target, seen, open, now, false)); got != nil {
		t.Fatalf("second check should be quiet, got %v", got)
	}
}

func TestRollingWindowIsSilent(t *testing.T) {
	w := store.Watch{Name: "w", Baselined: true, LastCheckAt: now.Add(-24 * time.Hour)}
	seen := seenOf("2026-10-27T10:00", "2026-11-01T10:00")
	open := []booksy.Slot{slot("2026-10-27T10:00"), slot("2026-11-01T10:00"), slot("2026-11-02T09:00")}
	r := Diff(w, target, seen, open, now, false)
	if got := opened(t, r); got != nil {
		t.Fatalf("a new day at the end of the window is not news: %v", got)
	}
	if len(r.Present) != 3 {
		t.Fatalf("the new day must still be recorded: %v", r.Present)
	}
}

func TestClosedSlotIsForgottenAndCanReopen(t *testing.T) {
	w := store.Watch{Name: "w", Baselined: true, LastCheckAt: now.Add(-15 * time.Minute)}
	seen := seenOf("2026-10-27T10:00", "2026-10-29T10:00")
	r := Diff(w, target, seen, []booksy.Slot{slot("2026-10-29T10:00")}, now, false)
	if len(r.Removed) != 1 || r.Removed[0] != "2026-10-27T10:00" || len(r.Events) != 0 {
		t.Fatalf("%+v", r)
	}
	seen = seenOf("2026-10-29T10:00")
	if got := opened(t, Diff(w, target, seen, []booksy.Slot{slot("2026-10-27T10:00"), slot("2026-10-29T10:00")}, now, false)); len(got) != 1 {
		t.Fatalf("reopened slot should fire: %v", got)
	}
}

func TestEmptySeenUsesLastCheckHorizon(t *testing.T) {
	// Fully booked last time: nothing seen. A slot inside the window the last
	// check covered is news; one past it is the window rolling.
	w := store.Watch{Name: "w", Baselined: true, LastCheckAt: now.Add(-time.Hour)}
	got := opened(t, Diff(w, target, nil, []booksy.Slot{slot("2026-10-28T10:00"), slot("2026-11-05T10:00")}, now, false))
	if len(got) != 1 || got[0] != "2026-10-28T10:00" {
		t.Fatalf("%v", got)
	}
}
