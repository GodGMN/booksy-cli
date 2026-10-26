// Package watch runs one Check for a Watch: fetch the current open slots,
// diff them against what the store already has, and produce Events. It does
// not deliver or persist anything; the CLI layer sequences sinks then commit.
package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/store"
)

// EventSlotsOpened is the only event type: one per Check that found new slots.
const EventSlotsOpened = "slots.opened"

// SlotsTarget is the JSON stored in a slots watch row.
type SlotsTarget struct {
	BusinessID int               `json:"business_id"`
	Business   string            `json:"business"`
	URL        string            `json:"url,omitempty"`
	VariantID  int               `json:"variant_id"`
	Service    string            `json:"service"`
	StafferID  int               `json:"staffer_id,omitempty"`
	Staffer    string            `json:"staffer,omitempty"`
	Days       int               `json:"days"`
	Filter     booksy.SlotFilter `json:"filter"`
}

// Opened is the payload of a slots.opened event.
type Opened struct {
	BusinessID int           `json:"business_id"`
	Business   string        `json:"business"`
	URL        string        `json:"url,omitempty"`
	Service    string        `json:"service"`
	VariantID  int           `json:"variant_id"`
	Staffer    string        `json:"staffer,omitempty"`
	Earliest   booksy.Slot   `json:"earliest"`
	Slots      []booksy.Slot `json:"slots"`
}

// Result is everything a Check produced, ready for delivery and commit.
type Result struct {
	Events  []store.Event
	Present []string
	Removed []string
	// Open is every slot that matched the filter on this check.
	Open []booksy.Slot
}

// Check runs one check. emitInitial makes the first run report every open
// slot instead of silently recording a baseline.
func Check(ctx context.Context, c *booksy.Client, st *store.Store, w store.Watch, now time.Time, emitInitial bool) (Result, error) {
	if w.Kind != store.KindSlots {
		return Result{}, fmt.Errorf("watch %s has unknown kind %q", w.Name, w.Kind)
	}
	var t SlotsTarget
	if err := json.Unmarshal(w.Target, &t); err != nil {
		return Result{}, fmt.Errorf("watch %s has a corrupt target: %w", w.Name, err)
	}
	seen, err := st.SeenFor(ctx, w.ID)
	if err != nil {
		return Result{}, err
	}
	days := t.Days
	if days < 1 {
		days = 14
	}
	from := now
	slots, err := c.Slots(ctx, booksy.SlotsQuery{BusinessID: t.BusinessID, VariantID: t.VariantID, StafferID: t.StafferID, From: from, To: from.AddDate(0, 0, days-1)})
	if err != nil {
		return Result{}, err
	}
	open := t.Filter.Apply(slots)
	return Diff(w, t, seen, open, now, emitInitial), nil
}

// Diff is the pure part of Check, split out for tests.
//
// A slot is news when it was not open on the previous check and it falls on
// or before the last date the previous check could see. Slots past that
// horizon are just the booking window rolling forward a day, which would
// otherwise fire every morning, so they are recorded silently.
func Diff(w store.Watch, t SlotsTarget, seen map[string]store.Seen, open []booksy.Slot, now time.Time, emitInitial bool) Result {
	baseline := !w.Baselined && !emitInitial
	horizon := ""
	for k := range seen {
		if d := k[:min(10, len(k))]; d > horizon {
			horizon = d
		}
	}
	if horizon == "" && !w.LastCheckAt.IsZero() {
		days := t.Days
		if days < 1 {
			days = 14
		}
		horizon = w.LastCheckAt.In(now.Location()).AddDate(0, 0, days-1).Format(time.DateOnly)
	}

	res := Result{Open: open}
	current := map[string]bool{}
	var fresh []booksy.Slot
	for _, s := range open {
		current[s.Start] = true
		res.Present = append(res.Present, s.Start)
		if _, known := seen[s.Start]; known || baseline {
			continue
		}
		if !emitInitial && horizon != "" && s.Date > horizon {
			continue
		}
		fresh = append(fresh, s)
	}
	// A slot that closes and later reopens should fire again, so closed
	// slots are forgotten rather than kept.
	for k := range seen {
		if !current[k] {
			res.Removed = append(res.Removed, k)
		}
	}
	sort.Strings(res.Removed)
	if len(fresh) == 0 {
		return res
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Start < fresh[j].Start })
	payload := Opened{
		BusinessID: t.BusinessID, Business: t.Business, URL: t.URL, Service: t.Service, VariantID: t.VariantID,
		Staffer: t.Staffer, Earliest: fresh[0], Slots: fresh,
	}
	data, _ := json.Marshal(payload)
	res.Events = append(res.Events, store.Event{Type: EventSlotsOpened, Watch: w.Name, Profile: w.Profile, At: now.UTC(), Data: data})
	return res
}

// Jitter spreads checks so several watches on one timer do not align. Up to
// 10% of the interval, never negative.
func Jitter(interval time.Duration, seed int64) time.Duration {
	if interval <= 0 {
		return 0
	}
	max := int64(interval / 10)
	if max == 0 {
		return 0
	}
	if seed < 0 {
		seed = -seed
	}
	return time.Duration(seed % max)
}
