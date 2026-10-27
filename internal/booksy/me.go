package booksy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Me is the logged-in customer. Personal fields stay as Booksy returns them;
// nothing here is cached beyond the profile's display name.
type Me struct {
	ID        int     `json:"id"`
	FirstName string  `json:"first_name"`
	LastName  string  `json:"last_name"`
	Email     string  `json:"email"`
	Phone     string  `json:"phone,omitempty"`
	City      string  `json:"city,omitempty"`
	Lat       float64 `json:"lat,omitempty"`
	Lng       float64 `json:"lng,omitempty"`
	Language  string  `json:"language,omitempty"`
}

func (c *Client) Me(ctx context.Context) (Me, error) {
	if err := c.requireToken("GET /me"); err != nil {
		return Me{}, err
	}
	var resp struct {
		Customer struct {
			ID        int      `json:"id"`
			FirstName string   `json:"first_name"`
			LastName  string   `json:"last_name"`
			Email     string   `json:"email"`
			CellPhone string   `json:"cell_phone"`
			City      string   `json:"city"`
			Latitude  *float64 `json:"latitude"`
			Longitude *float64 `json:"longitude"`
			Language  string   `json:"language"`
		} `json:"customer"`
	}
	if err := c.call(ctx, "GET", "/me", nil, nil, &resp); err != nil {
		return Me{}, err
	}
	r := resp.Customer
	if r.ID == 0 {
		return Me{}, &Error{Kind: KindAPIChanged, Endpoint: "GET /me", Msg: "the /me response has no customer"}
	}
	m := Me{ID: r.ID, FirstName: r.FirstName, LastName: r.LastName, Email: r.Email, Phone: r.CellPhone, City: r.City, Language: r.Language}
	if r.Latitude != nil && r.Longitude != nil {
		m.Lat, m.Lng = *r.Latitude, *r.Longitude
	}
	return m, nil
}

// Appointment is one booking on the account, identified by its uid.
type Appointment struct {
	UID        int64    `json:"uid"`
	Status     string   `json:"status"`
	From       string   `json:"from"`
	Till       string   `json:"till"`
	Timezone   string   `json:"timezone,omitempty"`
	BusinessID int      `json:"business_id"`
	Business   string   `json:"business"`
	URL        string   `json:"url,omitempty"`
	Services   []string `json:"services"`
	VariantIDs []int    `json:"variant_ids,omitempty"`
	Staff      []string `json:"staff,omitempty"`
	Total      string   `json:"total,omitempty"`
	Note       string   `json:"note,omitempty"`
	CanCancel  bool     `json:"can_cancel"`
	CanChange  bool     `json:"can_change"`
	// Version is Booksy's optimistic-lock counter, required to cancel.
	Version int64 `json:"version"`
}

// statusNames maps Booksy's one-letter codes to the words the CLI uses.
var statusNames = map[string]string{
	"A": "confirmed",
	"C": "cancelled",
	"F": "finished",
	"N": "no_show",
	"W": "awaiting_confirmation",
	"P": "proposed",
	"R": "rejected",
	"D": "declined",
	"M": "modified",
	// O: booked, but awaiting a prepayment, or a card guarantee that
	// drafts/schedule cannot attach (BookWithCard does).
	"O": "awaiting_payment",
	"V": "awaiting_account_verification",
	"X": "unknown",
}

func statusName(code string) string {
	if n, ok := statusNames[code]; ok {
		return n
	}
	return strings.ToLower(code)
}

type rawAppointment struct {
	AppointmentUID int64   `json:"appointment_uid"`
	Version        int64   `json:"_version"`
	Status         string  `json:"status"`
	BookedFrom     string  `json:"booked_from"`
	BookedTill     string  `json:"booked_till"`
	TimezoneName   string  `json:"timezone_name"`
	Total          string  `json:"total"`
	CustomerNote   *string `json:"customer_note"`
	Actions        struct {
		Cancel bool `json:"cancel"`
		Change bool `json:"change"`
	} `json:"actions"`
	Business struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"business"`
	// List entries carry service and resources at the top level.
	Service *struct {
		Name    string `json:"name"`
		Variant struct {
			ID int `json:"id"`
		} `json:"variant"`
	} `json:"service"`
	Resources []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"resources"`
	// Detail responses carry subbookings instead.
	Subbookings []struct {
		Service struct {
			Name    string `json:"name"`
			Variant struct {
				ID int `json:"id"`
			} `json:"variant"`
		} `json:"service"`
		Staffer *struct {
			Name string `json:"name"`
		} `json:"staffer"`
	} `json:"subbookings"`
}

func (c *Client) appointment(r rawAppointment) Appointment {
	a := Appointment{
		UID: r.AppointmentUID, Status: statusName(r.Status), From: r.BookedFrom, Till: r.BookedTill, Timezone: r.TimezoneName,
		BusinessID: r.Business.ID, Business: strings.TrimSpace(r.Business.Name), URL: c.businessURL(r.Business.URL),
		Total: nbsp(r.Total), CanCancel: r.Actions.Cancel, CanChange: r.Actions.Change, Version: r.Version, Services: []string{},
	}
	if r.CustomerNote != nil {
		a.Note = *r.CustomerNote
	}
	if r.Service != nil {
		a.Services = append(a.Services, r.Service.Name)
		a.VariantIDs = append(a.VariantIDs, r.Service.Variant.ID)
	}
	for _, res := range r.Resources {
		if res.Type == "S" {
			a.Staff = append(a.Staff, res.Name)
		}
	}
	for _, s := range r.Subbookings {
		a.Services = append(a.Services, s.Service.Name)
		a.VariantIDs = append(a.VariantIDs, s.Service.Variant.ID)
		if s.Staffer != nil {
			a.Staff = append(a.Staff, s.Staffer.Name)
		}
	}
	return a
}

// Upcoming reports whether the appointment is still ahead and not cancelled.
func (a Appointment) Upcoming(now time.Time) bool {
	if a.Status == "cancelled" || a.Status == "finished" || a.Status == "no_show" || a.Status == "rejected" || a.Status == "declined" {
		return false
	}
	loc := time.Local
	if a.Timezone != "" {
		if l, err := time.LoadLocation(a.Timezone); err == nil {
			loc = l
		}
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", a.From, loc)
	return err == nil && t.After(now)
}

// Appointments returns every booking on the account, newest first, walking pages.
func (c *Client) Appointments(ctx context.Context, limit int) ([]Appointment, error) {
	if err := c.requireToken("GET /me/bookings"); err != nil {
		return nil, err
	}
	out := []Appointment{}
	const per = 50
	for page := 1; ; page++ {
		q := url.Values{"bookings_page": {strconv.Itoa(page)}, "bookings_per_page": {strconv.Itoa(per)}}
		var resp struct {
			Bookings      []rawAppointment `json:"bookings"`
			BookingsCount int              `json:"bookings_count"`
		}
		if err := c.call(ctx, "GET", "/me/bookings", q, nil, &resp); err != nil {
			return nil, err
		}
		for _, b := range resp.Bookings {
			out = append(out, c.appointment(b))
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
		if len(resp.Bookings) < per || len(out) >= resp.BookingsCount {
			return out, nil
		}
	}
}

func (c *Client) Appointment(ctx context.Context, uid int64) (Appointment, error) {
	if err := c.requireToken("GET /me/appointments/{id}/"); err != nil {
		return Appointment{}, err
	}
	var resp struct {
		Appointment *rawAppointment `json:"appointment"`
	}
	q := url.Values{"no_thumbs": {"true"}, "with_combos": {"1"}}
	if err := c.call(ctx, "GET", fmt.Sprintf("/me/appointments/%d/", uid), q, nil, &resp); err != nil {
		if e, ok := err.(*Error); ok && e.Kind == KindNotFound {
			e.Msg = fmt.Sprintf("appointment %d not found on this account", uid)
		}
		return Appointment{}, err
	}
	if resp.Appointment == nil {
		return Appointment{}, &Error{Kind: KindAPIChanged, Endpoint: "GET /me/appointments/{id}/", Msg: "appointment response has no appointment"}
	}
	return c.appointment(*resp.Appointment), nil
}

// CancelAppointment cancels uid. version must be the appointment's current
// Version; Booksy rejects stale ones so a cancel never races a reschedule.
func (c *Client) CancelAppointment(ctx context.Context, uid, version int64) (Appointment, error) {
	if err := c.requireToken("POST /me/appointments/{id}/action/"); err != nil {
		return Appointment{}, err
	}
	var resp struct {
		Appointment *rawAppointment `json:"appointment"`
	}
	body := map[string]any{"action": "cancel", "_version": version, "no_thumbs": "true"}
	if err := c.call(ctx, "POST", fmt.Sprintf("/me/appointments/%d/action/", uid), nil, body, &resp); err != nil {
		return Appointment{}, err
	}
	if resp.Appointment == nil {
		return c.Appointment(ctx, uid)
	}
	return c.appointment(*resp.Appointment), nil
}

func (c *Client) Bookmarks(ctx context.Context) ([]BusinessSummary, error) {
	if err := c.requireToken("GET /me/bookmarks/"); err != nil {
		return nil, err
	}
	var resp struct {
		Businesses []rawBusiness `json:"businesses"`
	}
	if err := c.call(ctx, "GET", "/me/bookmarks/", url.Values{"no_thumbs": {"true"}}, nil, &resp); err != nil {
		return nil, err
	}
	out := []BusinessSummary{}
	for _, b := range resp.Businesses {
		out = append(out, c.summary(b))
	}
	return out, nil
}

func (c *Client) SetBookmark(ctx context.Context, businessID int, on bool) error {
	method := "PUT"
	if !on {
		method = "DELETE"
	}
	if err := c.requireToken(method + " /me/bookmarks/{id}/"); err != nil {
		return err
	}
	return c.call(ctx, method, fmt.Sprintf("/me/bookmarks/%d/", businessID), nil, nil, nil)
}

func (c *Client) MyReviews(ctx context.Context, page int) (ReviewPage, error) {
	if err := c.requireToken("GET /me/reviews/"); err != nil {
		return ReviewPage{}, err
	}
	if page < 1 {
		page = 1
	}
	q := url.Values{"reviews_page": {strconv.Itoa(page)}, "reviews_per_page": {"20"}}
	return c.reviews(ctx, "/me/reviews/", q, page)
}
