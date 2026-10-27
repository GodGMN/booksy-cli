package booksy

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// A business with no-show protection takes a cancellation fee only for a late
// cancellation or a no-show, but holds the booking as awaiting payment until a
// card guarantees it. booksy.com's widget sends no card for that: with a fee
// and no prepayment it just schedules, and Booksy binds the account's saved
// card itself (make_payment, the widget's prepayment call, refuses a fee-only
// booking). Without a saved card the booking stays awaiting payment and is
// cancelled later, so the CLI checks for one before booking:
//
//	GET me/payments/payment_options/?business_id=  the saved cards
//
// Cards live at Stripe; the CLI never sees or sends card details.

// Card is a saved card offered for one business.
type Card struct {
	Brand   string `json:"brand"`
	Last4   string `json:"last4"`
	Default bool   `json:"default"`
}

func (c Card) String() string {
	if c.Brand == "" {
		return "card •••• " + c.Last4
	}
	return c.Brand + " •••• " + c.Last4
}

// Cards lists the active saved cards Booksy offers for paying at businessID.
func (c *Client) Cards(ctx context.Context, businessID int) ([]Card, error) {
	if err := c.requireToken("GET /me/payments/payment_options/"); err != nil {
		return nil, err
	}
	var resp struct {
		PaymentOptions *[]struct {
			Type     string `json:"type"`
			IsActive bool   `json:"is_active"`
			Data     struct {
				ID      string `json:"id"`
				Default bool   `json:"default"`
				Details struct {
					Brand      string `json:"brand"`
					LastDigits string `json:"last_digits"`
				} `json:"details"`
			} `json:"data"`
		} `json:"payment_options"`
	}
	q := url.Values{"business_id": {strconv.Itoa(businessID)}}
	if err := c.call(ctx, "GET", "/me/payments/payment_options/", q, nil, &resp); err != nil {
		return nil, err
	}
	if resp.PaymentOptions == nil {
		return nil, &Error{Kind: KindAPIChanged, Endpoint: "GET /me/payments/payment_options/", Msg: "payment options response has no payment_options list"}
	}
	cards := []Card{}
	for _, o := range *resp.PaymentOptions {
		if o.Type != "card" || !o.IsActive || o.Data.ID == "" {
			continue
		}
		cards = append(cards, Card{Brand: o.Data.Details.Brand, Last4: o.Data.Details.LastDigits, Default: o.Data.Default})
	}
	return cards, nil
}

// PickCard is the card Booksy uses: the default one, else the first.
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

// Confirmation polling after schedule: Booksy binds the saved card to the
// cancellation fee on its own, shortly after the booking is made, and only
// then moves the appointment from awaiting payment to confirmed. Tests
// shorten it.
var ConfirmPoll = struct{ Every, For time.Duration }{3 * time.Second, 60 * time.Second}
