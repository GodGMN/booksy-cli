package booksy

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Slot is one bookable start time. Start is local to the business, in the
// form 2026-10-28T13:00, which is also what `booksy book --at` takes.
type Slot struct {
	Date  string `json:"date"`
	Time  string `json:"time"`
	Start string `json:"start"`
	// Promo is Booksy's per-slot promotion marker, when any.
	Promo string `json:"promo,omitempty"`
}

type SlotsQuery struct {
	BusinessID int
	VariantID  int
	// StafferID -1 means any staffer, as on booksy.com.
	StafferID int
	From, To  time.Time
}

// Slots returns open start times between From and To inclusive (dates only).
// Booksy caps the window at the business's booking horizon, usually a month.
func (c *Client) Slots(ctx context.Context, q SlotsQuery) ([]Slot, error) {
	if q.StafferID == 0 {
		q.StafferID = -1
	}
	body := map[string]any{
		"subbookings": []map[string]int{{"service_variant_id": q.VariantID, "staffer_id": q.StafferID}},
		"start_date":  q.From.Format(time.DateOnly),
		"end_date":    q.To.Format(time.DateOnly),
	}
	var resp struct {
		TimeSlots []struct {
			Date  string `json:"date"`
			Slots []struct {
				T string `json:"t"`
				P string `json:"p"`
			} `json:"slots"`
		} `json:"time_slots"`
	}
	path := fmt.Sprintf("/me/businesses/%d/appointments/time_slots", q.BusinessID)
	if err := c.call(ctx, "POST", path, nil, body, &resp); err != nil {
		return nil, err
	}
	out := []Slot{}
	for _, d := range resp.TimeSlots {
		for _, s := range d.Slots {
			out = append(out, Slot{Date: d.Date, Time: s.T, Start: d.Date + "T" + s.T, Promo: s.P})
		}
	}
	return out, nil
}

// SlotFilter narrows a slot list client-side.
type SlotFilter struct {
	// Between is "HH:MM-HH:MM", inclusive of the start, exclusive of the end.
	Between string `json:"between,omitempty"`
	// Weekdays are ISO (1 Monday .. 7 Sunday). Empty means every day.
	Weekdays []int `json:"weekdays,omitempty"`
	// Before keeps slots strictly before this local start (2026-10-30T18:00 or 2026-10-30).
	Before string `json:"before,omitempty"`
}

func (f SlotFilter) Validate() error {
	if f.Between != "" {
		if _, _, err := parseBetween(f.Between); err != nil {
			return err
		}
	}
	for _, d := range f.Weekdays {
		if d < 1 || d > 7 {
			return Usage("weekday %d out of range (1 Monday .. 7 Sunday)", d)
		}
	}
	if f.Before != "" {
		if _, err := normalizeStart(f.Before); err != nil {
			return err
		}
	}
	return nil
}

func (f SlotFilter) Apply(slots []Slot) []Slot {
	lo, hi, _ := parseBetween(f.Between)
	before, _ := normalizeStart(f.Before)
	days := map[int]bool{}
	for _, d := range f.Weekdays {
		days[d] = true
	}
	out := []Slot{}
	for _, s := range slots {
		if f.Between != "" && (s.Time < lo || s.Time >= hi) {
			continue
		}
		if before != "" && s.Start >= before {
			continue
		}
		if len(days) > 0 {
			d, err := time.Parse(time.DateOnly, s.Date)
			if err != nil || !days[isoWeekday(int(d.Weekday()))] {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func parseBetween(s string) (string, string, error) {
	if s == "" {
		return "", "", nil
	}
	lo, hi, ok := strings.Cut(s, "-")
	if !ok {
		return "", "", Usage("--between wants HH:MM-HH:MM, got %q", s)
	}
	lo, err1 := normalizeClock(lo)
	hi, err2 := normalizeClock(hi)
	if err1 != nil || err2 != nil || lo >= hi {
		return "", "", Usage("--between wants HH:MM-HH:MM with start before end, got %q", s)
	}
	return lo, hi, nil
}

func normalizeClock(s string) (string, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		if t, err = time.Parse("15", strings.TrimSpace(s)); err != nil {
			return "", err
		}
	}
	return t.Format("15:04"), nil
}

// normalizeStart accepts 2026-10-28T13:00, "2026-10-28 13:00" or a bare date
// (meaning its midnight) and returns the canonical 2026-10-28T13:00.
func normalizeStart(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05", time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02T15:04"), nil
		}
	}
	return "", Usage("%q is not a date or date and time (use 2026-10-28T13:00)", s)
}

// ParseStart is normalizeStart for callers outside the package.
func ParseStart(s string) (date, clock string, err error) {
	s = strings.TrimSpace(s)
	n, err := normalizeStart(s)
	if err != nil {
		return "", "", err
	}
	if len(s) <= len("2006-01-02") {
		return "", "", Usage("%q has no time; use 2026-10-28T13:00", s)
	}
	return n[:10], n[11:], nil
}
