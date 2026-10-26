package booksy

import (
	"strings"
	"testing"
)

func TestParseWindow(t *testing.T) {
	cases := map[string]Window{
		"mon-fri 16:00-close":    {Weekdays: []int{1, 2, 3, 4, 5}, From: "16:00", Till: "24:00"},
		"sat":                    {Weekdays: []int{6}, From: "00:00", Till: "24:00"},
		"lun,mié 9-13":           {Weekdays: []int{1, 3}, From: "09:00", Till: "13:00"},
		"weekdays 18:00-":        {Weekdays: []int{1, 2, 3, 4, 5}, From: "18:00", Till: "24:00"},
		"sat-mon open-12:00":     {Weekdays: []int{6, 7, 1}, From: "00:00", Till: "12:00"},
		"2026-12-24 10:00-12:00": {Dates: []string{"2026-12-24"}, From: "10:00", Till: "12:00"},
		"2026-12-31 off":         {Dates: []string{"2026-12-31"}, From: "00:00", Till: "24:00", Off: true},
	}
	for in, want := range cases {
		got, err := ParseWindow(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if strings.Join(itoa(got.Weekdays), ",") != strings.Join(itoa(want.Weekdays), ",") || strings.Join(got.Dates, ",") != strings.Join(want.Dates, ",") ||
			got.From != want.From || got.Till != want.Till || got.Off != want.Off {
			t.Errorf("%q: got %+v want %+v", in, got, want)
		}
	}
	for _, bad := range []string{"", "funday", "mon 18:00-16:00", "mon 25:00-26:00", "mon 16:00", "2026-13-01", "mon 1-2 extra"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func itoa(xs []int) []string {
	var out []string
	for _, x := range xs {
		out = append(out, string(rune('0'+x)))
	}
	return out
}

func TestWindowsMatch(t *testing.T) {
	var ws []Window
	for _, s := range []string{"mon-fri 16:00-close", "sat", "2026-12-24 10:00-12:00", "2026-12-31 off"} {
		w, err := ParseWindow(s)
		if err != nil {
			t.Fatal(err)
		}
		ws = append(ws, w)
	}
	for start, want := range map[string]bool{
		"2026-12-01T15:59": false, // Tuesday, before 16:00
		"2026-12-01T16:00": true,
		"2026-12-01T19:30": true,
		"2026-12-05T09:00": true,  // Saturday, all day
		"2026-12-06T12:00": false, // Sunday, not listed
		"2026-12-24T11:00": true,  // Thursday, but the date window rules
		"2026-12-24T17:00": false, // would fit Mon-Fri, but the date overrides it
		"2026-12-31T17:00": false, // Thursday, day off
	} {
		s := Slot{Date: start[:10], Time: start[11:], Start: start}
		if got := (SlotFilter{Windows: ws}).Apply([]Slot{s}); (len(got) == 1) != want {
			t.Errorf("%s: want %v", start, want)
		}
	}
}
