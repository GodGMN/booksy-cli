package booksy

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Booking goes through the booking widget's draft API, the same sequence
// booksy.com runs:
//
//	POST drafts/create            a cart for one variant (anonymous works)
//	POST drafts/{id}/change_timeslot   pick the start time; validated server-side
//	POST drafts/{id}/add_customer_note optional
//	GET  drafts/{id}/confirmation      human summary (what the widget shows)
//	POST drafts/{id}/schedule          turns the draft into an appointment (needs a token)
//
// Drafts are throwaway (status X) and cost nothing; only schedule books.

// Draft is a booking cart. Version must be echoed on every change.
type Draft struct {
	ID         string        `json:"id"`
	Version    string        `json:"version"`
	Status     string        `json:"status"`
	BusinessID int           `json:"business_id"`
	Business   string        `json:"business"`
	From       string        `json:"from"`
	Till       string        `json:"till"`
	Duration   string        `json:"duration"`
	Total      string        `json:"total"`
	CoreID     int64         `json:"appointment_uid,omitempty"`
	Items      []DraftItem   `json:"items"`
	Summary    *Confirmation `json:"summary,omitempty"`
}

type DraftItem struct {
	ID       string `json:"id"`
	Service  string `json:"service"`
	Variant  int    `json:"variant_id"`
	Price    string `json:"price"`
	From     string `json:"from"`
	Till     string `json:"till"`
	Staffer  string `json:"staffer,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// Confirmation is the review screen's text, localized by Booksy.
type Confirmation struct {
	Header       string   `json:"header,omitempty"`
	Date         string   `json:"date"`
	BusinessInfo string   `json:"business_info"`
	Lines        []string `json:"lines"`
	Total        string   `json:"total,omitempty"`
	Policy       string   `json:"policy,omitempty"`
}

type rawDraft struct {
	Appointment struct {
		ID       string `json:"id"`
		Version  string `json:"version"`
		Status   string `json:"status"`
		Business struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"business"`
		BookedFrom  string `json:"booked_from"`
		BookedTill  string `json:"booked_till"`
		Duration    string `json:"duration"`
		Total       string `json:"total"`
		CoreID      *int64 `json:"core_id"`
		Subbookings []struct {
			ID         string `json:"id"`
			BookedFrom string `json:"booked_from"`
			BookedTill string `json:"booked_till"`
			Service    struct {
				Name    string `json:"name"`
				Price   string `json:"price"`
				Variant struct {
					ID       int    `json:"id"`
					Duration string `json:"duration"`
				} `json:"variant"`
			} `json:"service"`
			Staffer *struct {
				Name string `json:"name"`
			} `json:"staffer"`
		} `json:"subbookings"`
	} `json:"appointment"`
}

func (r rawDraft) draft() Draft {
	a := r.Appointment
	d := Draft{
		ID: a.ID, Version: a.Version, Status: a.Status, BusinessID: a.Business.ID, Business: strings.TrimSpace(a.Business.Name),
		From: a.BookedFrom, Till: a.BookedTill, Duration: a.Duration, Total: nbsp(a.Total), Items: []DraftItem{},
	}
	if a.CoreID != nil {
		d.CoreID = *a.CoreID
	}
	for _, s := range a.Subbookings {
		it := DraftItem{ID: s.ID, Service: s.Service.Name, Variant: s.Service.Variant.ID, Price: nbsp(s.Service.Price), From: s.BookedFrom, Till: s.BookedTill, Duration: s.Service.Variant.Duration}
		if s.Staffer != nil {
			it.Staffer = s.Staffer.Name
		}
		d.Items = append(d.Items, it)
	}
	return d
}

func nbsp(s string) string { return strings.ReplaceAll(s, " ", " ") }

// draftMeta is what the widget attaches to create and schedule calls.
func draftMeta() map[string]string {
	return map[string]string{"app_version": appVersion, "platform": "web", "device_type": "Desktop", "booking_source": "Business_Details"}
}

func (c *Client) draftCall(ctx context.Context, method, path string, body any) (Draft, error) {
	if c.Fingerprint == "" {
		return Draft{}, Usage("booking needs a device fingerprint; this profile has none. Run `booksy auth login` again")
	}
	var raw rawDraft
	if err := c.call(ctx, method, path, nil, body, &raw); err != nil {
		return Draft{}, err
	}
	if raw.Appointment.ID == "" {
		return Draft{}, &Error{Kind: KindAPIChanged, Endpoint: method + " " + templatePath("/core/v2/customer_api"+path), Msg: "draft response has no appointment id"}
	}
	return raw.draft(), nil
}

// CreateDraft opens a cart for one variant. stafferID -1 means no preference.
func (c *Client) CreateDraft(ctx context.Context, businessID, variantID, stafferID int) (Draft, error) {
	if stafferID == 0 {
		stafferID = -1
	}
	return c.draftCall(ctx, "POST", "/drafts/create", map[string]any{
		"business_id": businessID, "service_variant_id": variantID, "staffer_id": stafferID, "meta": draftMeta(),
	})
}

// EditDraft opens a cart pre-filled from an existing appointment, for a reschedule.
func (c *Client) EditDraft(ctx context.Context, appointmentUID int64) (Draft, error) {
	if err := c.requireToken("POST /drafts/edit_appointment"); err != nil {
		return Draft{}, err
	}
	return c.draftCall(ctx, "POST", "/drafts/edit_appointment", map[string]any{"appointment_id": appointmentUID, "meta": draftMeta()})
}

// ChangeTimeslot moves the draft to date (2026-10-28) and clock (13:00).
// Booksy refuses a time that is not open with a validation error.
func (c *Client) ChangeTimeslot(ctx context.Context, d Draft, date, clock string) (Draft, error) {
	nd, err := c.draftCall(ctx, "POST", "/drafts/"+d.ID+"/change_timeslot", map[string]any{
		"slot": map[string]string{"d": date, "t": clock}, "version": d.Version,
	})
	if e, ok := err.(*Error); ok && e.Kind == KindUsage {
		e.Msg = fmt.Sprintf("%sT%s is not available for this service (see `booksy slots`)", date, clock)
		e.Reason = "slot_unavailable"
	}
	return nd, err
}

// ChangeStaffer assigns a staffer to the draft's first item.
func (c *Client) ChangeStaffer(ctx context.Context, d Draft, stafferID int) (Draft, error) {
	if len(d.Items) == 0 {
		return d, nil
	}
	return c.draftCall(ctx, "POST", "/drafts/"+d.ID+"/change_staffer", map[string]any{
		"version": d.Version, "subbooking_id": d.Items[0].ID, "staffer_id": stafferID,
	})
}

func (c *Client) AddNote(ctx context.Context, d Draft, note string) (Draft, error) {
	return c.draftCall(ctx, "POST", "/drafts/"+d.ID+"/add_customer_note", map[string]any{"customer_note": note, "version": d.Version})
}

func (c *Client) Confirmation(ctx context.Context, d Draft) (Confirmation, error) {
	var raw struct {
		Header         string `json:"header"`
		Date           string `json:"date"`
		BusinessInfo   string `json:"business_info"`
		ServiceSummary []struct {
			Name        string `json:"name"`
			VariantName string `json:"variant_name"`
			Price       string `json:"price"`
			StafferInfo string `json:"staffer_info"`
			TimeInfo    string `json:"time_info"`
		} `json:"service_summary"`
		TotalSummary *struct {
			Price string `json:"price"`
		} `json:"total_summary"`
		AdditionalInfo *struct {
			Text string `json:"text"`
		} `json:"additional_info"`
	}
	if err := c.call(ctx, "GET", "/drafts/"+d.ID+"/confirmation", nil, nil, &raw); err != nil {
		return Confirmation{}, err
	}
	out := Confirmation{Header: raw.Header, Date: nbsp(raw.Date), BusinessInfo: raw.BusinessInfo, Lines: []string{}}
	if raw.TotalSummary != nil {
		out.Total = nbsp(raw.TotalSummary.Price)
	}
	if raw.AdditionalInfo != nil {
		out.Policy = raw.AdditionalInfo.Text
	}
	for _, s := range raw.ServiceSummary {
		name := strings.TrimSpace(s.Name + " " + s.VariantName)
		parts := []string{name, nbsp(s.Price), s.TimeInfo, strings.Join(strings.Fields(strings.Replace(s.StafferInfo, ":", ": ", 1)), " ")}
		var kept []string
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				kept = append(kept, p)
			}
		}
		out.Lines = append(out.Lines, strings.Join(kept, " · "))
	}
	return out, nil
}

// Payment is what Booksy will ask for, from payments/calc on a draft.
// Amounts are decimal strings in the business's currency, as Booksy sends them.
type Payment struct {
	// Prepayment must be paid by card before the booking is confirmed.
	Prepayment string `json:"prepayment"`
	// CancellationFee is charged only for a late cancellation or a no-show.
	CancellationFee string `json:"cancellation_fee"`
	// ToPayLater is paid at the business after the service.
	ToPayLater string `json:"to_pay_later"`
}

// NeedsPrepayment reports a prepayment above zero, which only booksy.com's
// card form can settle.
func (p Payment) NeedsPrepayment() bool { return positive(p.Prepayment) }

// HasCancellationFee reports a no-show/late-cancellation fee above zero.
func (p Payment) HasCancellationFee() bool { return positive(p.CancellationFee) }

func positive(s string) bool {
	f, ok := amount(s)
	return ok && f > 0
}

func amount(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
	return f, err == nil
}

// PaymentCalc prices the draft, as booksy.com's review screen does before
// schedule. A prepayment or a cancellation fee above zero means the booking
// needs a card (to pay, or to guarantee the fee); without one Booksy keeps
// it in status O and later cancels it.
func (c *Client) PaymentCalc(ctx context.Context, d Draft) (Payment, error) {
	if err := c.requireToken("POST /me/payments/calc/"); err != nil {
		return Payment{}, err
	}
	var raw struct {
		AppointmentPayment *struct {
			PrepaymentTotal      string `json:"prepayment_total"`
			CancellationFeeTotal string `json:"cancellation_fee_total"`
			ToPayLater           string `json:"to_pay_later"`
		} `json:"appointment_payment"`
	}
	if err := c.call(ctx, "POST", "/me/payments/calc/", nil, map[string]any{"draft_id": d.ID}, &raw); err != nil {
		return Payment{}, err
	}
	if raw.AppointmentPayment == nil {
		return Payment{}, &Error{Kind: KindAPIChanged, Endpoint: "POST /me/payments/calc/", Msg: "payment calculation has no appointment_payment"}
	}
	p := raw.AppointmentPayment
	// The card check below depends on these two, so an amount that is
	// missing or not a number stops the booking instead of reading as zero.
	for field, v := range map[string]string{"prepayment_total": p.PrepaymentTotal, "cancellation_fee_total": p.CancellationFeeTotal} {
		if _, ok := amount(v); !ok {
			return Payment{}, &Error{Kind: KindAPIChanged, Endpoint: "POST /me/payments/calc/", Msg: fmt.Sprintf("payment calculation has no readable %s, so the CLI cannot tell whether this booking needs a card", field)}
		}
	}
	return Payment{Prepayment: p.PrepaymentTotal, CancellationFee: p.CancellationFeeTotal, ToPayLater: p.ToPayLater}, nil
}

// Schedule books the draft. This is the only call in the flow with an effect.
func (c *Client) Schedule(ctx context.Context, d Draft) (Draft, error) {
	if err := c.requireToken("POST /drafts/{id}/schedule"); err != nil {
		return Draft{}, err
	}
	return c.draftCall(ctx, "POST", "/drafts/"+d.ID+"/schedule", map[string]any{"meta": draftMeta()})
}
