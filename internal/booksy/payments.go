package booksy

import (
	"context"
	"fmt"
	"net/url"
)

// Card is a payment card saved on the account (booksy.com, Account →
// Payments). The CLI never sees card numbers; Booksy only lists brand and
// last digits.
type Card struct {
	ID      int64  `json:"id"`
	Brand   string `json:"brand"`
	Last4   string `json:"last4"`
	Default bool   `json:"default"`
}

func (c Card) String() string { return fmt.Sprintf("%s ··%s", c.Brand, c.Last4) }

// Cards lists the account's usable saved cards.
func (c *Client) Cards(ctx context.Context) ([]Card, error) {
	if err := c.requireToken("GET /me/payment_methods/"); err != nil {
		return nil, err
	}
	var resp struct {
		PaymentMethods []struct {
			ID         int64  `json:"payment_method_id"`
			Confirmed  bool   `json:"confirmed"`
			Active     bool   `json:"active"`
			Default    bool   `json:"default"`
			MethodType string `json:"method_type"`
			CardType   string `json:"card_type"`
			LastDigits string `json:"card_last_digits"`
		} `json:"payment_methods"`
	}
	q := url.Values{"page": {"1"}, "per_page": {"99"}}
	if err := c.call(ctx, "GET", "/me/payment_methods/", q, nil, &resp); err != nil {
		return nil, err
	}
	out := []Card{}
	for _, m := range resp.PaymentMethods {
		if m.ID == 0 || !m.Active || !m.Confirmed || m.MethodType != "credit_card" {
			continue
		}
		out = append(out, Card{ID: m.ID, Brand: m.CardType, Last4: m.LastDigits, Default: m.Default})
	}
	return out, nil
}

// PickCard returns the default card, or the first one when none is marked.
func PickCard(cards []Card) (Card, bool) {
	for _, c := range cards {
		if c.Default {
			return c, true
		}
	}
	if len(cards) > 0 {
		return cards[0], true
	}
	return Card{}, false
}

// BookWithCard books a single-service draft through the appointment API
// booksy.com uses for businesses with no-show protection:
//
//	POST me/appointments/business/{id}/   {subbookings, payment_method, ...}
//
// drafts/{id}/schedule cannot attach a card, so a booking made there stays
// awaiting payment and Booksy cancels it. Here the card's payment_method_id
// goes with the booking and Booksy authorizes the fee on it (charged only
// for a late cancellation or a no-show). The draft only supplies the
// validated time, staffer and variant.
// guaranteed reports that Booksy registered the fee against the card.
func (c *Client) BookWithCard(ctx context.Context, d Draft, note string, card Card) (a Appointment, guaranteed bool, err error) {
	const ep = "POST /me/appointments/business/{id}/"
	if err := c.requireToken(ep); err != nil {
		return Appointment{}, false, err
	}
	if len(d.Items) != 1 {
		return Appointment{}, false, Usage("booking with a card supports one service per booking, this draft has %d", len(d.Items))
	}
	it := d.Items[0]
	staffer := it.StafferID
	if staffer == 0 {
		staffer = -1
	}
	from := it.From
	if from == "" {
		from = d.From
	}
	body := map[string]any{
		"subbookings": []map[string]any{{
			"staffer_id":      staffer,
			"booked_from":     from,
			"service_variant": map[string]any{"mode": "variant", "id": it.Variant},
			"combo_children":  []any{},
		}},
		"is_cancellation_fee_preauth_flow": false,
		"pre_auth_type":                    "control",
		"customer_note":                    note,
		"service_questions":                []any{},
		"bci_agreements":                   map[string]bool{"web_communication_agreement": false, "disclosure_obligation_agreement": false},
		"ask_for_consent":                  false,
		"payment_method":                   card.ID,
		"compatibilities":                  map[string]bool{"prepayment": true},
	}
	var resp struct {
		Appointment *struct {
			rawAppointment
			PaymentInfo *struct {
				DepositID *int64 `json:"deposit_id"`
			} `json:"payment_info"`
		} `json:"appointment"`
	}
	if err := c.call(ctx, "POST", fmt.Sprintf("/me/appointments/business/%d/", d.BusinessID), nil, body, &resp); err != nil {
		return Appointment{}, false, err
	}
	if resp.Appointment == nil || resp.Appointment.AppointmentUID == 0 {
		return Appointment{}, false, &Error{Kind: KindAPIChanged, Endpoint: ep, Msg: "booking response has no appointment"}
	}
	p := resp.Appointment.PaymentInfo
	return c.appointment(resp.Appointment.rawAppointment), p != nil && p.DepositID != nil, nil
}
