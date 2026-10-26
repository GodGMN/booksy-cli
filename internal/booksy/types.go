package booksy

import (
	"encoding/json"
	"strings"
)

// The exported types are the CLI's JSON contract: snake_case, stable, and
// trimmed to what a person or script needs. raw* types mirror Booksy's wire
// format and never leave this package.

type Category struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// BusinessSummary is one search hit, or the head of a full Business.
type BusinessSummary struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	URL       string  `json:"url"`
	Address   string  `json:"address,omitempty"`
	City      string  `json:"city,omitempty"`
	Lat       float64 `json:"lat,omitempty"`
	Lng       float64 `json:"lng,omitempty"`
	DistanceM int     `json:"distance_m,omitempty"`
	Rating    float64 `json:"rating"`
	Reviews   int     `json:"reviews"`
	Category  int     `json:"category,omitempty"`
	Promoted  bool    `json:"promoted,omitempty"`
	// Services is filled by search only: the services that matched the query.
	Services []Service `json:"services,omitempty"`
}

type Business struct {
	BusinessSummary
	Description   string     `json:"description,omitempty"`
	Website       string     `json:"website,omitempty"`
	Instagram     string     `json:"instagram,omitempty"`
	Facebook      string     `json:"facebook,omitempty"`
	BookingPolicy string     `json:"booking_policy,omitempty"`
	OpenHours     []OpenDay  `json:"open_hours"`
	Amenities     []string   `json:"amenities,omitempty"`
	Services      []Service  `json:"services"`
	Staff         []Staffer  `json:"staff"`
	Categories    []Category `json:"service_categories,omitempty"`
}

type OpenDay struct {
	// Weekday follows ISO 8601: 1 is Monday, 7 is Sunday.
	Weekday int    `json:"weekday"`
	From    string `json:"from"`
	Till    string `json:"till"`
}

type Service struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category,omitempty"`
	Description string    `json:"description,omitempty"`
	Variants    []Variant `json:"variants"`
	StafferIDs  []int     `json:"staffer_ids,omitempty"`
	Bookable    bool      `json:"bookable"`
}

// Variant is what actually gets booked: a Service at one duration and price.
type Variant struct {
	ID          int     `json:"id"`
	Label       string  `json:"label,omitempty"`
	DurationMin int     `json:"duration_min"`
	Price       float64 `json:"price"`
	PriceText   string  `json:"price_text,omitempty"`
	// PriceType is Booksy's code: X fixed, S starts at, V varies, F free, D don't show.
	PriceType  string `json:"price_type,omitempty"`
	StafferIDs []int  `json:"staffer_ids,omitempty"`
}

type Staffer struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Position string `json:"position,omitempty"`
	PhotoURL string `json:"photo_url,omitempty"`
}

type Review struct {
	ID              int      `json:"id"`
	Rating          int      `json:"rating"`
	Title           string   `json:"title,omitempty"`
	Text            string   `json:"text,omitempty"`
	Author          string   `json:"author"`
	Verified        bool     `json:"verified"`
	Services        []string `json:"services,omitempty"`
	Staff           []string `json:"staff,omitempty"`
	AppointmentDate string   `json:"appointment_date,omitempty"`
	Created         string   `json:"created"`
	Reply           string   `json:"reply,omitempty"`
	BusinessID      int      `json:"business_id,omitempty"`
	BusinessName    string   `json:"business_name,omitempty"`
}

// Place is a resolved free-text location.
type Place struct {
	Query      string  `json:"query"`
	Label      string  `json:"label"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
	LocationID int     `json:"location_id,omitempty"`
}

// wire types

type rawCoordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type rawLocation struct {
	Coordinate rawCoordinate `json:"coordinate"`
	City       string        `json:"city"`
	Address    string        `json:"address"`
}

type rawVariant struct {
	ID           int     `json:"id"`
	Label        string  `json:"label"`
	Duration     int     `json:"duration"`
	Price        float64 `json:"price"`
	Type         string  `json:"type"`
	ServicePrice string  `json:"service_price"`
	StafferID    []int   `json:"staffer_id"`
}

type rawService struct {
	ID                            int          `json:"id"`
	Name                          string       `json:"name"`
	Description                   string       `json:"description"`
	Active                        *bool        `json:"active"`
	StafferID                     []int        `json:"staffer_id"`
	Variants                      []rawVariant `json:"variants"`
	IsAvailableForCustomerBooking *bool        `json:"is_available_for_customer_booking"`
}

type rawServiceCategory struct {
	ID       int          `json:"id"`
	Name     string       `json:"name"`
	Services []rawService `json:"services"`
}

type rawStaffer struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Position string `json:"position"`
	PhotoURL string `json:"photo_url"`
	Visible  *bool  `json:"visible"`
	Active   *bool  `json:"active"`
}

type rawBusiness struct {
	ID                int                  `json:"id"`
	Name              string               `json:"name"`
	URL               string               `json:"url"`
	Description       string               `json:"description"`
	Location          rawLocation          `json:"location"`
	Distance          *float64             `json:"distance"`
	ReviewsRank       float64              `json:"reviews_rank"`
	ReviewsCount      int                  `json:"reviews_count"`
	PrimaryCategory   int                  `json:"primary_category"`
	Promoted          bool                 `json:"promoted"`
	ServiceCategories []rawServiceCategory `json:"service_categories"`
	Staff             []rawStaffer         `json:"staff"`
	Website           string               `json:"website"`
	FacebookLink      string               `json:"facebook_link"`
	InstagramLink     string               `json:"instagram_link"`
	BookingPolicy     json.RawMessage      `json:"booking_policy"`
	OpenHours         []struct {
		DayOfWeek int    `json:"day_of_week"`
		OpenFrom  string `json:"open_from"`
		OpenTill  string `json:"open_till"`
	} `json:"open_hours"`
	Amenities []struct {
		Label string `json:"label"`
	} `json:"amenities"`
}

func (c *Client) businessURL(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http") {
		return path
	}
	return "https://booksy.com/" + Locale(c.Country) + "/" + strings.TrimLeft(path, "/")
}

// Locale maps a country to the booksy.com path prefix used in public URLs.
func Locale(country string) string {
	switch country {
	case "es":
		return "es-es"
	case "pl":
		return "pl-pl"
	case "br":
		return "pt-br"
	case "pt":
		return "pt-pt"
	case "fr":
		return "fr-fr"
	case "gb", "uk":
		return "en-gb"
	case "ie":
		return "en-ie"
	case "za":
		return "en-za"
	case "mx":
		return "es-mx"
	case "us":
		return "en-us"
	}
	return "en-" + country
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func (c *Client) summary(b rawBusiness) BusinessSummary {
	s := BusinessSummary{
		ID:       b.ID,
		Name:     strings.TrimSpace(b.Name),
		URL:      c.businessURL(b.URL),
		Address:  b.Location.Address,
		City:     b.Location.City,
		Lat:      b.Location.Coordinate.Latitude,
		Lng:      b.Location.Coordinate.Longitude,
		Rating:   round2(b.ReviewsRank),
		Reviews:  b.ReviewsCount,
		Category: b.PrimaryCategory,
		Promoted: b.Promoted,
	}
	if b.Distance != nil {
		s.DistanceM = int(*b.Distance)
	}
	return s
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func convertServices(cats []rawServiceCategory) []Service {
	out := []Service{}
	for _, cat := range cats {
		for _, s := range cat.Services {
			if !boolOr(s.Active, true) {
				continue
			}
			svc := Service{
				ID:          s.ID,
				Name:        strings.TrimSpace(s.Name),
				Category:    cat.Name,
				Description: strings.TrimSpace(s.Description),
				StafferIDs:  s.StafferID,
				Bookable:    boolOr(s.IsAvailableForCustomerBooking, true),
				Variants:    []Variant{},
			}
			for _, v := range s.Variants {
				svc.Variants = append(svc.Variants, Variant{
					ID:          v.ID,
					Label:       v.Label,
					DurationMin: v.Duration,
					Price:       v.Price,
					PriceText:   strings.ReplaceAll(v.ServicePrice, " ", " "),
					PriceType:   v.Type,
					StafferIDs:  v.StafferID,
				})
			}
			out = append(out, svc)
		}
	}
	return out
}

func convertStaff(raw []rawStaffer) []Staffer {
	out := []Staffer{}
	for _, s := range raw {
		if !boolOr(s.Visible, true) || !boolOr(s.Active, true) {
			continue
		}
		out = append(out, Staffer{ID: s.ID, Name: strings.TrimSpace(s.Name), Position: s.Position, PhotoURL: s.PhotoURL})
	}
	return out
}

func (c *Client) business(b rawBusiness) Business {
	out := Business{
		BusinessSummary: c.summary(b),
		Description:     strings.TrimSpace(b.Description),
		Website:         b.Website,
		Instagram:       b.InstagramLink,
		Facebook:        b.FacebookLink,
		Services:        convertServices(b.ServiceCategories),
		Staff:           convertStaff(b.Staff),
		OpenHours:       []OpenDay{},
	}
	var policy string
	if json.Unmarshal(b.BookingPolicy, &policy) == nil {
		out.BookingPolicy = policy
	}
	for _, h := range b.OpenHours {
		out.OpenHours = append(out.OpenHours, OpenDay{Weekday: isoWeekday(h.DayOfWeek), From: h.OpenFrom, Till: h.OpenTill})
	}
	for _, a := range b.Amenities {
		out.Amenities = append(out.Amenities, a.Label)
	}
	for _, cat := range b.ServiceCategories {
		out.Categories = append(out.Categories, Category{ID: cat.ID, Name: cat.Name})
	}
	return out
}

// isoWeekday: Booksy uses 0 for Sunday and 1..6 for Monday..Saturday.
func isoWeekday(d int) int {
	if d == 0 {
		return 7
	}
	return d
}

// Variant looks up a variant by id across all services.
func (b Business) Variant(id int) (Service, Variant, bool) {
	for _, s := range b.Services {
		for _, v := range s.Variants {
			if v.ID == id {
				return s, v, true
			}
		}
	}
	return Service{}, Variant{}, false
}

// StafferName returns the staffer's name, or "" when unknown.
func (b Business) StafferName(id int) string {
	for _, s := range b.Staff {
		if s.ID == id {
			return s.Name
		}
	}
	return ""
}
