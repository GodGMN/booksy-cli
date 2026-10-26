package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GodGMN/booksy-cli/internal/booksy"
	"github.com/GodGMN/booksy-cli/internal/output"
)

// slotFlags is shared by slots, book --first and watch add slots.
type slotFlags struct {
	service  string
	staff    string
	from     string
	days     int
	between  string
	weekdays string
	before   string
	when     []string
}

func (s *slotFlags) bind(cmd *cobra.Command, days int) {
	f := cmd.Flags()
	f.StringVar(&s.service, "service", "", "variant id, service id, or part of the service name")
	f.StringVar(&s.staff, "staff", "", "staffer id or name (default: anyone)")
	f.StringVar(&s.from, "from", "", "first day to look at, YYYY-MM-DD (default today)")
	f.IntVar(&s.days, "days", days, "how many days to look ahead (booksy caps this, usually at a month)")
	f.StringVar(&s.between, "between", "", "only start times in HH:MM-HH:MM, e.g. 17:00-20:00")
	f.StringVar(&s.weekdays, "weekdays", "", "only these days: mon,tue,... or 1-5 (1 is Monday)")
	f.StringVar(&s.before, "before", "", "only slots starting before this date or date and time")
	f.StringArrayVar(&s.when, "when", nil, `per-day schedule, repeatable: "mon-fri 16:00-close", "sat", "2026-12-24 10:00-12:00", "2026-12-31 off"`)
}

func (s slotFlags) filter() (booksy.SlotFilter, error) {
	f := booksy.SlotFilter{Between: s.between, Before: s.before}
	days, err := parseWeekdays(s.weekdays)
	if err != nil {
		return f, err
	}
	f.Weekdays = days
	for _, w := range s.when {
		win, err := booksy.ParseWindow(w)
		if err != nil {
			return f, err
		}
		f.Windows = append(f.Windows, win)
	}
	return f, f.Validate()
}

// parseWeekdays reads --weekdays; empty means every day.
func parseWeekdays(s string) ([]int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	days, err := booksy.ParseWeekdays(s)
	if err != nil {
		return nil, output.Usagef("--weekdays: %v", err)
	}
	return days, nil
}

func (s slotFlags) window(now time.Time) (time.Time, time.Time, error) {
	from := now
	if s.from != "" {
		t, err := time.ParseInLocation(time.DateOnly, s.from, time.Local)
		if err != nil {
			return from, from, output.Usagef("--from wants YYYY-MM-DD, got %q", s.from)
		}
		from = t
	}
	days := s.days
	if days < 1 {
		return from, from, output.Usagef("--days must be at least 1")
	}
	return from, from.AddDate(0, 0, days-1), nil
}

// resolveStaff maps --staff to an id and display name. Empty means anyone.
func resolveStaff(b booksy.Business, ref string) (int, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == "any" || ref == "-1" {
		return -1, "", nil
	}
	if id, err := strconv.Atoi(ref); err == nil {
		if name := b.StafferName(id); name != "" {
			return id, name, nil
		}
		return 0, "", booksy.NotFound("business %d has no staffer %d (see `booksy business staff %d`)", b.ID, id, b.ID)
	}
	var hits []booksy.Staffer
	for _, s := range b.Staff {
		if strings.EqualFold(s.Name, ref) {
			return s.ID, s.Name, nil
		}
		if strings.Contains(strings.ToLower(s.Name), strings.ToLower(ref)) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return 0, "", booksy.NotFound("business %d has no staffer matching %q (see `booksy business staff %d`)", b.ID, ref, b.ID)
	case 1:
		return hits[0].ID, hits[0].Name, nil
	}
	var names []string
	for _, h := range hits {
		names = append(names, fmt.Sprintf("%d (%s)", h.ID, h.Name))
	}
	return 0, "", booksy.Usage("%q matches several staff: %s", ref, strings.Join(names, ", "))
}

// SlotsResult is the JSON contract of `booksy slots`.
type SlotsResult struct {
	BusinessID int           `json:"business_id"`
	Business   string        `json:"business"`
	Service    string        `json:"service"`
	VariantID  int           `json:"variant_id"`
	Duration   int           `json:"duration_min"`
	Price      string        `json:"price,omitempty"`
	StafferID  int           `json:"staffer_id,omitempty"`
	Staffer    string        `json:"staffer,omitempty"`
	From       string        `json:"from"`
	To         string        `json:"to"`
	Slots      []booksy.Slot `json:"slots"`
}

func (r SlotsResult) Pretty(w io.Writer, color bool) {
	head := fmt.Sprintf("%s · %s (%dmin", r.Business, r.Service, r.Duration)
	if r.Price != "" {
		head += ", " + r.Price
	}
	head += ")"
	if r.Staffer != "" {
		head += " with " + r.Staffer
	}
	fmt.Fprintln(w, head)
	if len(r.Slots) == 0 {
		fmt.Fprintln(w, output.Dim(fmt.Sprintf("no open slots between %s and %s", r.From, r.To), color))
		return
	}
	byDay := map[string][]string{}
	var order []string
	for _, s := range r.Slots {
		if _, ok := byDay[s.Date]; !ok {
			order = append(order, s.Date)
		}
		byDay[s.Date] = append(byDay[s.Date], s.Time)
	}
	for _, d := range order {
		label := d
		if t, err := time.Parse(time.DateOnly, d); err == nil {
			label = t.Format("Mon 02 Jan")
		}
		times := byDay[d]
		fmt.Fprintf(w, "%s  %s\n", label, wrapTimes(times, 12, len(label)+2))
	}
	fmt.Fprintln(w, output.Dim(fmt.Sprintf("%d slots on %d days", len(r.Slots), len(order)), color))
}

func wrapTimes(times []string, perLine, indent int) string {
	var b strings.Builder
	for i, t := range times {
		if i > 0 {
			if i%perLine == 0 {
				b.WriteString("\n" + strings.Repeat(" ", indent))
			} else {
				b.WriteString(" ")
			}
		}
		b.WriteString(t)
	}
	return b.String()
}

// findSlots resolves business, variant and staff and returns filtered slots.
func (a *App) findSlots(ctx context.Context, ref string, sf slotFlags) (booksy.Business, SlotsResult, error) {
	b, err := a.loadBusiness(ctx, ref)
	if err != nil {
		return b, SlotsResult{}, err
	}
	res, err := a.slotsFor(ctx, b, sf)
	return b, res, err
}

func (a *App) slotsFor(ctx context.Context, b booksy.Business, sf slotFlags) (SlotsResult, error) {
	svc, v, err := b.ResolveVariant(sf.service)
	if err != nil {
		return SlotsResult{}, err
	}
	staffID, staffName, err := resolveStaff(b, sf.staff)
	if err != nil {
		return SlotsResult{}, err
	}
	filter, err := sf.filter()
	if err != nil {
		return SlotsResult{}, err
	}
	from, to, err := sf.window(time.Now())
	if err != nil {
		return SlotsResult{}, err
	}
	slots, err := a.Client.Slots(ctx, booksy.SlotsQuery{BusinessID: b.ID, VariantID: v.ID, StafferID: staffID, From: from, To: to})
	if err != nil {
		return SlotsResult{}, err
	}
	res := SlotsResult{
		BusinessID: b.ID, Business: b.Name, Service: serviceLabel(svc, v), VariantID: v.ID, Duration: v.DurationMin, Price: v.PriceText,
		Staffer: staffName, From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), Slots: filter.Apply(slots),
	}
	if staffID > 0 {
		res.StafferID = staffID
	}
	return res, nil
}

func serviceLabel(s booksy.Service, v booksy.Variant) string {
	if v.Label != "" {
		return s.Name + " · " + v.Label
	}
	return s.Name
}

func (a *App) slotsCmd() *cobra.Command {
	var sf slotFlags
	var first bool
	cmd := &cobra.Command{
		Use:   "slots <business>",
		Short: "Open start times for one service",
		Long: `List the start times Booksy would offer for a service, the same list the
booking widget shows. No login needed.

--service takes a variant id (from ` + "`booksy business services`" + `), a service id when the
service has a single variant, or part of the service name.`,
		Example: `  booksy slots 3349 --service corte --format pretty
  booksy slots 3349 --service 273164 --staff Javier --days 14 --between 17:00-20:00
  booksy slots 3349 --service corte --weekdays sat --first`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, res, err := a.findSlots(cmd.Context(), args[0], sf)
			if err != nil {
				return err
			}
			if first && len(res.Slots) > 1 {
				res.Slots = res.Slots[:1]
			}
			if a.Printer.Format == output.JSONL {
				return a.Printer.Print(res.Slots)
			}
			return a.Printer.Print(res)
		},
	}
	sf.bind(cmd, 7)
	cmd.Flags().BoolVar(&first, "first", false, "only the earliest matching slot")
	return cmd
}
