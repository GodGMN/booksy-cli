package booksy

import (
	"strings"
	"time"
)

// Window is one entry of a weekly schedule: some days, and a time range on
// them. It is how a caller says "Mon-Fri from 16:00 to closing, and Saturday
// all day" in one filter instead of several queries merged by hand.
//
// Dates, when set, name specific days and take precedence over weekday
// windows for those days, so "2026-12-24 10:00-12:00" or "2026-12-31 off"
// adjusts one day without restating the week.
type Window struct {
	Weekdays []int    `json:"weekdays,omitempty"` // ISO, 1 Monday .. 7 Sunday
	Dates    []string `json:"dates,omitempty"`    // YYYY-MM-DD
	From     string   `json:"from"`               // HH:MM, inclusive
	Till     string   `json:"till"`               // HH:MM, exclusive; 24:00 is closing
	Off      bool     `json:"off,omitempty"`      // the day is excluded
}

var weekdayWords = map[string]int{
	"mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6, "sun": 7,
	"lun": 1, "mar": 2, "mie": 3, "mié": 3, "jue": 4, "vie": 5, "sab": 6, "sáb": 6, "dom": 7,
}

// ParseWeekday accepts 1..7, English or Spanish names and their first three letters.
func ParseWeekday(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 1 && s[0] >= '1' && s[0] <= '7' {
		return int(s[0] - '0')
	}
	if d, ok := weekdayWords[s]; ok {
		return d
	}
	if r := []rune(s); len(r) >= 3 {
		return weekdayWords[string(r[:3])]
	}
	return 0
}

// ParseWeekdays reads "mon,wed", "mon-fri", "1-5" or a mix.
func ParseWeekdays(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, b := ParseWeekday(lo), ParseWeekday(hi)
			if a == 0 || b == 0 {
				return nil, Usage("bad day range %q", part)
			}
			// mon-sun wraps: sat-mon is Sat, Sun, Mon.
			for d := a; ; d = d%7 + 1 {
				out = append(out, d)
				if d == b {
					break
				}
			}
			continue
		}
		d := ParseWeekday(part)
		if d == 0 {
			return nil, Usage("unknown day %q (use mon..sun, lun..dom or 1..7)", part)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, Usage("no days given")
	}
	return out, nil
}

// ParseWindow reads one --when value:
//
//	"mon-fri 16:00-close"   weekdays, from 16:00 to closing time
//	"sat"                   all day
//	"sat 10:00-14:00"
//	"daily 18:00-"          an open end means until closing
//	"2026-12-24 10:00-12:00"  one date, overriding its weekday windows
//	"2026-12-31 off"        exclude a date
//
// open and close stand for the start and end of the day: Booksy only offers
// slots inside opening hours, so they are the business's own hours.
func ParseWindow(s string) (Window, error) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 || len(fields) > 2 {
		return Window{}, Usage("--when %q: want DAYS [HH:MM-HH:MM], e.g. \"mon-fri 16:00-close\" or \"sat\"", s)
	}
	w := Window{From: "00:00", Till: "24:00"}
	days := fields[0]
	switch {
	case days == "daily" || days == "any" || days == "all":
		w.Weekdays = []int{1, 2, 3, 4, 5, 6, 7}
	case days == "weekdays":
		w.Weekdays = []int{1, 2, 3, 4, 5}
	case days == "weekend":
		w.Weekdays = []int{6, 7}
	case isDateList(days):
		for _, d := range strings.Split(days, ",") {
			if _, err := time.Parse(time.DateOnly, d); err != nil {
				return Window{}, Usage("--when %q: %q is not a YYYY-MM-DD date", s, d)
			}
			w.Dates = append(w.Dates, d)
		}
	default:
		wd, err := ParseWeekdays(days)
		if err != nil {
			return Window{}, Usage("--when %q: %v", s, err)
		}
		w.Weekdays = wd
	}
	if len(fields) == 1 {
		return w, nil
	}
	rng := strings.ToLower(fields[1])
	if rng == "off" || rng == "none" || rng == "closed" {
		w.Off = true
		return w, nil
	}
	lo, hi, ok := strings.Cut(rng, "-")
	if !ok {
		return Window{}, Usage("--when %q: time range wants HH:MM-HH:MM", s)
	}
	var err error
	if w.From, err = clockOr(lo, "open", "00:00"); err != nil {
		return Window{}, Usage("--when %q: %q is not a time", s, lo)
	}
	if w.Till, err = clockOr(hi, "close", "24:00"); err != nil {
		return Window{}, Usage("--when %q: %q is not a time", s, hi)
	}
	if w.From >= w.Till {
		return Window{}, Usage("--when %q: the range must end after it starts", s)
	}
	return w, nil
}

func isDateList(s string) bool {
	return len(s) >= 10 && s[4] == '-' && s[7] == '-' && s[0] >= '0' && s[0] <= '9'
}

func clockOr(s, word, value string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == word {
		return value, nil
	}
	return normalizeClock(s)
}

// matchWindows reports whether a slot fits the schedule. Date windows for
// the slot's date replace the weekday windows for that day.
func matchWindows(ws []Window, s Slot) bool {
	if len(ws) == 0 {
		return true
	}
	var dated []Window
	for _, w := range ws {
		for _, d := range w.Dates {
			if d == s.Date {
				dated = append(dated, w)
			}
		}
	}
	day := 0
	if t, err := time.Parse(time.DateOnly, s.Date); err == nil {
		day = isoWeekday(int(t.Weekday()))
	}
	applies := func(w Window) bool {
		if w.Off || s.Time < w.From || s.Time >= w.Till {
			return false
		}
		return true
	}
	if len(dated) > 0 {
		for _, w := range dated {
			if applies(w) {
				return true
			}
		}
		return false
	}
	for _, w := range ws {
		if len(w.Dates) > 0 {
			continue
		}
		for _, d := range w.Weekdays {
			if d == day && applies(w) {
				return true
			}
		}
	}
	return false
}
