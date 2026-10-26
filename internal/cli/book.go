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

// BookingResult is the JSON contract of `book` and `appointment reschedule`.
type BookingResult struct {
	Booked      bool                `json:"booked"`
	DryRun      bool                `json:"dry_run"`
	Draft       booksy.Draft        `json:"draft"`
	Summary     booksy.Confirmation `json:"summary"`
	Payment     *booksy.Payment     `json:"payment,omitempty"`
	Appointment *booksy.Appointment `json:"appointment,omitempty"`
}

func (r BookingResult) Pretty(w io.Writer, color bool) {
	switch {
	case r.Booked:
		fmt.Fprintln(w, bold("Booked.", color))
	case r.DryRun:
		fmt.Fprintln(w, bold("Dry run: nothing was booked.", color))
	}
	summaryPretty(w, r.Summary, color)
	paymentPretty(w, r.Payment)
	if r.Appointment != nil {
		fmt.Fprintf(w, "appointment %d · %s\n", r.Appointment.UID, r.Appointment.Status)
	}
}

func summaryPretty(w io.Writer, s booksy.Confirmation, color bool) {
	fmt.Fprintln(w, s.BusinessInfo)
	fmt.Fprintln(w, s.Date)
	for _, l := range s.Lines {
		fmt.Fprintln(w, "  "+l)
	}
	if s.Total != "" {
		fmt.Fprintln(w, "total "+s.Total)
	}
	if s.Policy != "" {
		fmt.Fprintln(w, output.Dim(s.Policy, color))
	}
}

func paymentPretty(w io.Writer, p *booksy.Payment) {
	if p == nil {
		return
	}
	if p.NeedsPrepayment() {
		fmt.Fprintf(w, "prepayment required: %s\n", p.Prepayment)
	}
	if p.ToPayLater != "" {
		fmt.Fprintf(w, "pay at the business: %s\n", p.ToPayLater)
	}
	if p.HasCancellationFee() {
		fmt.Fprintf(w, "cancellation fee: %s if you cancel late or do not show up\n", p.CancellationFee)
	}
}

func bold(s string, color bool) string {
	if !color {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

// bookingFlow carries a draft from creation to schedule, shared by book and
// reschedule so both confirm the same way.
func (a *App) finishBooking(ctx context.Context, d booksy.Draft, date, clock, note string, dryRun, yes bool, verb string) error {
	var err error
	if d, err = a.Client.ChangeTimeslot(ctx, d, date, clock); err != nil {
		return err
	}
	if note != "" {
		if d, err = a.Client.AddNote(ctx, d, note); err != nil {
			return err
		}
	}
	sum, err := a.Client.Confirmation(ctx, d)
	if err != nil {
		return err
	}
	res := BookingResult{DryRun: dryRun, Draft: d, Summary: sum}
	// Pricing needs a login; an anonymous dry run shows the rest.
	if a.Client.HasToken() {
		pay, err := a.Client.PaymentCalc(ctx, d)
		if err != nil {
			return err
		}
		res.Payment = &pay
		if !dryRun {
			if pay.NeedsPrepayment() {
				return booksy.Usage("%s asks for a %s prepayment by card, which only booksy.com can take. Book this one on booksy.com or in the app", d.Business, pay.Prepayment)
			}
			// No-show protection: the service is still paid in person, but
			// Booksy holds the booking as awaiting payment until a card is
			// registered as a guarantee, and cancels it if none comes. Card
			// entry is Stripe's form on booksy.com, so stop before booking.
			if pay.HasCancellationFee() {
				return booksy.Usage("%s uses no-show protection: you pay %s at the business, but booksy needs a card on file to guarantee a %s fee for late cancellations, and only booksy.com or the app can take it. Book this one there; a booking made here would stay unconfirmed and be cancelled", d.Business, pay.ToPayLater, pay.CancellationFee)
			}
		}
	}
	if dryRun {
		return a.Printer.Print(res)
	}
	if a.Interactive && !yes {
		summaryPretty(a.Stderr, sum, false)
		paymentPretty(a.Stderr, res.Payment)
	}
	if err := a.confirm(yes, verb+"?"); err != nil {
		return err
	}
	done, err := a.Client.Schedule(ctx, d)
	if err != nil {
		return err
	}
	res.Booked, res.Draft = true, done
	if done.CoreID != 0 {
		if ap, err := a.Client.Appointment(ctx, done.CoreID); err == nil {
			res.Appointment = &ap
			if ap.Status == "awaiting_payment" {
				a.notice("booked, but booksy left it awaiting payment instead of confirmed. Check it on booksy.com (My appointments), or cancel with `booksy appointment cancel %d`", ap.UID)
			}
		} else {
			a.notice("booked, but fetching the appointment failed: %v", err)
		}
	}
	return a.Printer.Print(res)
}

func (a *App) bookCmd() *cobra.Command {
	var sf slotFlags
	var at, note string
	var first, dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "book <business>",
		Short: "Book an appointment",
		Long: `Book one service at one start time, through the same draft flow as the
booking widget. The booking is shown and confirmed before it is made; pass --yes
to skip the prompt, or --dry-run to stop just before booking.

Pick the time with --at, or --first for the earliest slot that matches the
slot filters (--days, --between, --weekdays, --before, --staff).

Booking needs a login (` + "`booksy auth login`" + `); --dry-run does not.`,
		Example: `  booksy book 3349 --service corte --at 2026-10-30T18:00
  booksy book 3349 --service 273164 --staff Javier --first --between 17:00-20:00 --dry-run
  booksy book 3349 --service corte --at "2026-10-30 18:00" --note "primera vez" --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if (at == "") == !first {
				return output.Usagef("pass exactly one of --at or --first")
			}
			if !dryRun {
				if err := a.requireSession(); err != nil {
					return err
				}
			}
			if err := a.ensureFingerprint(); err != nil {
				return err
			}
			b, err := a.loadBusiness(ctx, args[0])
			if err != nil {
				return err
			}
			_, v, err := b.ResolveVariant(sf.service)
			if err != nil {
				return err
			}
			staffID, _, err := resolveStaff(b, sf.staff)
			if err != nil {
				return err
			}
			var date, clock string
			if first {
				res, err := a.slotsFor(ctx, b, sf)
				if err != nil {
					return err
				}
				if len(res.Slots) == 0 {
					return booksy.NotFound("no open slot matches between %s and %s", res.From, res.To)
				}
				date, clock = res.Slots[0].Date, res.Slots[0].Time
			} else if date, clock, err = booksy.ParseStart(at); err != nil {
				return err
			}
			d, err := a.Client.CreateDraft(ctx, b.ID, v.ID, staffID)
			if err != nil {
				return err
			}
			if staffID > 0 && (len(d.Items) == 0 || d.Items[0].Staffer == "") {
				if d, err = a.Client.ChangeStaffer(ctx, d, staffID); err != nil {
					return err
				}
			}
			return a.finishBooking(ctx, d, date, clock, note, dryRun, yes, "Book this")
		},
	}
	sf.bind(cmd, 14)
	f := cmd.Flags()
	f.StringVar(&at, "at", "", "start time, 2026-10-30T18:00 (business local time)")
	f.BoolVar(&first, "first", false, "book the earliest slot matching the slot filters")
	f.StringVar(&note, "note", "", "note for the business")
	f.BoolVar(&dryRun, "dry-run", false, "prepare and show the booking, then stop")
	f.BoolVar(&yes, "yes", false, "book without asking")
	return cmd
}

// appointments

type appointmentsView []booksy.Appointment

func (v appointmentsView) Pretty(w io.Writer, color bool) {
	rows := [][]string{}
	for _, ap := range v {
		rows = append(rows, []string{strconv.FormatInt(ap.UID, 10), strings.Replace(ap.From, "T", " ", 1), ap.Status, output.Truncate(ap.Business, 30), output.Truncate(strings.Join(ap.Services, ", "), 30), ap.Total})
	}
	output.Table(w, color, []string{"UID", "WHEN", "STATUS", "BUSINESS", "SERVICE", "TOTAL"}, rows)
}

type appointmentView struct{ booksy.Appointment }

func (v appointmentView) Pretty(w io.Writer, color bool) {
	ap := v.Appointment
	fmt.Fprintf(w, "%s  %s\n", bold(ap.Business, color), output.Dim(fmt.Sprintf("appointment %d", ap.UID), color))
	fmt.Fprintf(w, "%s – %s  %s\n", strings.Replace(ap.From, "T", " ", 1), ap.Till[strings.IndexByte(ap.Till, 'T')+1:], ap.Status)
	fmt.Fprintln(w, strings.Join(ap.Services, ", "))
	if len(ap.Staff) > 0 {
		fmt.Fprintln(w, "with "+strings.Join(ap.Staff, ", "))
	}
	if ap.Total != "" {
		fmt.Fprintln(w, "total "+ap.Total)
	}
	if ap.Note != "" {
		fmt.Fprintln(w, "note: "+ap.Note)
	}
	var can []string
	if ap.CanCancel {
		can = append(can, "cancel")
	}
	if ap.CanChange {
		can = append(can, "reschedule")
	}
	if len(can) > 0 {
		fmt.Fprintln(w, output.Dim("can: "+strings.Join(can, ", "), color))
	}
}

func parseUID(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, output.Usagef("%q is not an appointment uid (see `booksy appointment list`)", s)
	}
	return n, nil
}

func (a *App) appointmentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "appointment", Aliases: []string{"appointments"}, Short: "Your appointments: list, show, cancel, reschedule"}

	var upcoming, past bool
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "Appointments on the account, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireSession(); err != nil {
				return err
			}
			if upcoming && past {
				return output.Usagef("--upcoming and --past are exclusive")
			}
			fetch := limit
			if upcoming || past {
				fetch = 0
			}
			all, err := a.Client.Appointments(cmd.Context(), fetch)
			if err != nil {
				return err
			}
			now := time.Now()
			out := []booksy.Appointment{}
			for _, ap := range all {
				if (upcoming && !ap.Upcoming(now)) || (past && ap.Upcoming(now)) {
					continue
				}
				out = append(out, ap)
				if limit > 0 && len(out) >= limit {
					break
				}
			}
			return a.Printer.Print(appointmentsView(out))
		},
	}
	list.Flags().BoolVar(&upcoming, "upcoming", false, "only appointments still ahead")
	list.Flags().BoolVar(&past, "past", false, "only finished, cancelled or missed appointments")
	list.Flags().IntVar(&limit, "limit", 20, "at most this many (0 for all)")

	show := &cobra.Command{
		Use:   "show <uid>",
		Short: "One appointment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireSession(); err != nil {
				return err
			}
			uid, err := parseUID(args[0])
			if err != nil {
				return err
			}
			ap, err := a.Client.Appointment(cmd.Context(), uid)
			if err != nil {
				return err
			}
			return a.Printer.Print(appointmentView{ap})
		},
	}

	var yes bool
	cancel := &cobra.Command{
		Use:   "cancel <uid>",
		Short: "Cancel an appointment (asks first, or --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireSession(); err != nil {
				return err
			}
			uid, err := parseUID(args[0])
			if err != nil {
				return err
			}
			ap, err := a.Client.Appointment(cmd.Context(), uid)
			if err != nil {
				return err
			}
			if !ap.CanCancel {
				return booksy.Usage("booksy does not allow cancelling appointment %d (%s). The business's policy may require contacting them", uid, ap.Status)
			}
			if err := a.confirm(yes, fmt.Sprintf("Cancel %s at %s on %s?", strings.Join(ap.Services, ", "), ap.Business, strings.Replace(ap.From, "T", " ", 1))); err != nil {
				return err
			}
			done, err := a.Client.CancelAppointment(cmd.Context(), uid, ap.Version)
			if err != nil {
				return err
			}
			return a.Printer.Print(appointmentView{done})
		},
	}
	cancel.Flags().BoolVar(&yes, "yes", false, "cancel without asking")

	var at, note string
	var dryRun, ryes bool
	reschedule := &cobra.Command{
		Use:   "reschedule <uid>",
		Short: "Move an appointment to another start time",
		Long: `Move an appointment to another open start time, keeping its service and
staffer. Check times first with ` + "`booksy slots`" + `. Asks first, or --yes.`,
		Example: `  booksy appointment reschedule 123456789 --at 2026-12-05T18:00 --dry-run`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.requireSession(); err != nil {
				return err
			}
			if err := a.ensureFingerprint(); err != nil {
				return err
			}
			uid, err := parseUID(args[0])
			if err != nil {
				return err
			}
			date, clock, err := booksy.ParseStart(at)
			if err != nil {
				return err
			}
			ap, err := a.Client.Appointment(ctx, uid)
			if err != nil {
				return err
			}
			if !ap.CanChange {
				return booksy.Usage("booksy does not allow changing appointment %d (%s)", uid, ap.Status)
			}
			d, err := a.Client.EditDraft(ctx, uid)
			if err != nil {
				return err
			}
			return a.finishBooking(ctx, d, date, clock, note, dryRun, ryes, "Move it")
		},
	}
	reschedule.Flags().StringVar(&at, "at", "", "new start time, 2026-12-05T18:00 (required)")
	reschedule.Flags().StringVar(&note, "note", "", "replace the note for the business")
	reschedule.Flags().BoolVar(&dryRun, "dry-run", false, "prepare and show the change, then stop")
	reschedule.Flags().BoolVar(&ryes, "yes", false, "reschedule without asking")
	_ = reschedule.MarkFlagRequired("at")

	cmd.AddCommand(list, show, cancel, reschedule)
	return cmd
}
