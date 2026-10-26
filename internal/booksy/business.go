package booksy

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func (c *Client) Categories(ctx context.Context) ([]Category, error) {
	var resp struct {
		Categories []Category `json:"categories"`
	}
	if err := c.call(ctx, "GET", "/businesses/categories/", nil, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Categories, nil
}

// SearchParams is a business search. Exactly one of Geo or LocationID should
// be set; with neither, Booksy searches the whole country.
type SearchParams struct {
	Query      string  `json:"query,omitempty"`
	Category   int     `json:"category,omitempty"`
	Lat        float64 `json:"lat,omitempty"`
	Lng        float64 `json:"lng,omitempty"`
	LocationID int     `json:"location_id,omitempty"`
	// Sort is score (Booksy's relevance, the default) or distance.
	Sort    string `json:"sort,omitempty"`
	Page    int    `json:"page,omitempty"`
	PerPage int    `json:"per_page,omitempty"`
}

type SearchPage struct {
	Total      int               `json:"total"`
	Page       int               `json:"page"`
	PerPage    int               `json:"per_page"`
	Businesses []BusinessSummary `json:"businesses"`
}

func (c *Client) Search(ctx context.Context, p SearchParams) (SearchPage, error) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PerPage < 1 {
		p.PerPage = 20
	}
	q := url.Values{}
	q.Set("response_type", "listing_web")
	q.Set("no_thumbs", "true")
	q.Set("businesses_page", strconv.Itoa(p.Page))
	q.Set("businesses_per_page", strconv.Itoa(p.PerPage))
	if p.Query != "" {
		q.Set("query", p.Query)
	}
	if p.Category != 0 {
		q.Set("category", strconv.Itoa(p.Category))
	}
	if p.Lat != 0 || p.Lng != 0 {
		q.Set("location_geo", fmt.Sprintf("%.6f,%.6f", p.Lat, p.Lng))
	} else if p.LocationID != 0 {
		q.Set("location_id", strconv.Itoa(p.LocationID))
	}
	if p.Sort != "" {
		q.Set("sort_order", p.Sort)
	}
	var resp struct {
		Businesses      []rawBusiness `json:"businesses"`
		BusinessesCount int           `json:"businesses_count"`
	}
	if err := c.call(ctx, "GET", "/businesses", q, nil, &resp); err != nil {
		return SearchPage{}, err
	}
	page := SearchPage{Total: resp.BusinessesCount, Page: p.Page, PerPage: p.PerPage, Businesses: []BusinessSummary{}}
	for _, b := range resp.Businesses {
		s := c.summary(b)
		s.Services = convertServices(b.ServiceCategories)
		page.Businesses = append(page.Businesses, s)
	}
	return page, nil
}

func (c *Client) Business(ctx context.Context, id int) (Business, error) {
	var resp struct {
		Business *rawBusiness `json:"business"`
	}
	q := url.Values{"no_thumbs": {"true"}}
	if err := c.call(ctx, "GET", fmt.Sprintf("/businesses/%d", id), q, nil, &resp); err != nil {
		if e, ok := err.(*Error); ok && e.Kind == KindNotFound {
			e.Msg = fmt.Sprintf("business %d not found", id)
		}
		return Business{}, err
	}
	if resp.Business == nil {
		return Business{}, &Error{Kind: KindAPIChanged, Endpoint: "GET /businesses/{id}", Msg: "business response has no business object"}
	}
	return c.business(*resp.Business), nil
}

type rawReview struct {
	ID       int    `json:"id"`
	Rank     int    `json:"rank"`
	Title    string `json:"title"`
	Review   string `json:"review"`
	Verified bool   `json:"verified"`
	User     struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"user"`
	Services []struct {
		Name string `json:"name"`
	} `json:"services"`
	Staff []struct {
		Name string `json:"name"`
	} `json:"staff"`
	AppointmentDate string  `json:"appointment_date"`
	Created         string  `json:"created"`
	ReplyContent    *string `json:"reply_content"`
	Business        struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"business"`
}

func convertReview(r rawReview) Review {
	out := Review{
		ID: r.ID, Rating: r.Rank, Title: r.Title, Text: strings.TrimSpace(r.Review), Verified: r.Verified,
		Author:          strings.TrimSpace(r.User.FirstName + " " + r.User.LastName),
		AppointmentDate: r.AppointmentDate, Created: r.Created,
		BusinessID: r.Business.ID, BusinessName: r.Business.Name,
	}
	if r.ReplyContent != nil {
		out.Reply = *r.ReplyContent
	}
	for _, s := range r.Services {
		out.Services = append(out.Services, s.Name)
	}
	for _, s := range r.Staff {
		out.Staff = append(out.Staff, s.Name)
	}
	return out
}

type ReviewPage struct {
	Total   int      `json:"total"`
	Rating  float64  `json:"rating"`
	Page    int      `json:"page"`
	Reviews []Review `json:"reviews"`
}

func (c *Client) Reviews(ctx context.Context, businessID, page, perPage int) (ReviewPage, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	q := url.Values{"reviews_page": {strconv.Itoa(page)}, "reviews_per_page": {strconv.Itoa(perPage)}}
	return c.reviews(ctx, fmt.Sprintf("/businesses/%d/reviews", businessID), q, page)
}

func (c *Client) reviews(ctx context.Context, path string, q url.Values, page int) (ReviewPage, error) {
	var resp struct {
		Reviews      []rawReview `json:"reviews"`
		ReviewsCount int         `json:"reviews_count"`
		ReviewsRank  float64     `json:"reviews_rank"`
	}
	if err := c.call(ctx, "GET", path, q, nil, &resp); err != nil {
		return ReviewPage{}, err
	}
	out := ReviewPage{Total: resp.ReviewsCount, Rating: round2(resp.ReviewsRank), Page: page, Reviews: []Review{}}
	for _, r := range resp.Reviews {
		out.Reviews = append(out.Reviews, convertReview(r))
	}
	return out, nil
}

// ResolvePlace turns free text ("Valencia", "Calle Mayor 1, Madrid") into
// coordinates using the same resolver as the booksy.com search box.
func (c *Client) ResolvePlace(ctx context.Context, query string) (Place, error) {
	var resp struct {
		Match *struct {
			Latitude         float64 `json:"latitude"`
			Longitude        float64 `json:"longitude"`
			FormattedAddress string  `json:"formatted_address"`
		} `json:"match"`
		Location *struct {
			ID int `json:"id"`
		} `json:"location"`
		FullLabel string `json:"full_label"`
	}
	err := c.call(ctx, "GET", "/search/street_hints/resolve/", url.Values{"query": {query}}, nil, &resp)
	if e, ok := err.(*Error); ok && e.Kind == KindUsage {
		return Place{}, Usage("booksy cannot find a place called %q", query)
	}
	if err != nil {
		return Place{}, err
	}
	if resp.Match == nil {
		return Place{}, Usage("booksy cannot find a place called %q", query)
	}
	p := Place{Query: query, Label: resp.Match.FormattedAddress, Lat: resp.Match.Latitude, Lng: resp.Match.Longitude}
	if p.Label == "" {
		p.Label = resp.FullLabel
	}
	if resp.Location != nil {
		p.LocationID = resp.Location.ID
	}
	return p, nil
}

var (
	bareID  = regexp.MustCompile(`^\d+$`)
	slugID  = regexp.MustCompile(`(?:^|/)(\d+)_[^/]*`)
	widgetB = regexp.MustCompile(`[?&#]business(?:_id)?=(\d+)`)
)

// ParseBusinessRef accepts a numeric id, a booksy.com business URL
// (https://booksy.com/es-es/3349_barberia-..._madrid) or its path slug.
func ParseBusinessRef(ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if bareID.MatchString(ref) {
		return strconv.Atoi(ref)
	}
	if m := slugID.FindStringSubmatch(ref); m != nil {
		return strconv.Atoi(m[1])
	}
	if m := widgetB.FindStringSubmatch(ref); m != nil {
		return strconv.Atoi(m[1])
	}
	return 0, Usage("%q is not a business id or booksy.com business URL", ref)
}

// ResolveVariant picks the variant a caller means. ref may be a variant id, a
// service id with a single variant, or a case-insensitive substring of the
// service name. Ambiguity is an error listing the candidates.
func (b Business) ResolveVariant(ref string) (Service, Variant, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		if len(b.Services) == 1 && len(b.Services[0].Variants) == 1 {
			return b.Services[0], b.Services[0].Variants[0], nil
		}
		return Service{}, Variant{}, Usage("pick a service with --service (see `booksy business services %d`)", b.ID)
	}
	if id, err := strconv.Atoi(ref); err == nil {
		if s, v, ok := b.Variant(id); ok {
			return s, v, nil
		}
		for _, s := range b.Services {
			if s.ID == id {
				if len(s.Variants) == 1 {
					return s, s.Variants[0], nil
				}
				return Service{}, Variant{}, Usage("service %d (%s) has %d variants; pass one of: %s", id, s.Name, len(s.Variants), variantList(s))
			}
		}
		return Service{}, Variant{}, NotFound("business %d has no service or variant %d", b.ID, id)
	}
	needle := strings.ToLower(ref)
	type cand struct {
		s Service
		v Variant
	}
	var exact, partial []cand
	for _, s := range b.Services {
		name := strings.ToLower(s.Name)
		for _, v := range s.Variants {
			full := strings.TrimSpace(name + " " + strings.ToLower(v.Label))
			switch {
			case name == needle || full == needle:
				exact = append(exact, cand{s, v})
			case strings.Contains(full, needle):
				partial = append(partial, cand{s, v})
			}
		}
	}
	pick := exact
	if len(pick) == 0 {
		pick = partial
	}
	switch len(pick) {
	case 0:
		return Service{}, Variant{}, NotFound("business %d has no service matching %q (see `booksy business services %d`)", b.ID, ref, b.ID)
	case 1:
		return pick[0].s, pick[0].v, nil
	}
	var names []string
	for i, c := range pick {
		if i == 8 {
			names = append(names, "…")
			break
		}
		names = append(names, fmt.Sprintf("%d (%s%s)", c.v.ID, c.s.Name, labelSuffix(c.v)))
	}
	return Service{}, Variant{}, Usage("%q matches %d services; pass a variant id: %s", ref, len(pick), strings.Join(names, ", "))
}

func labelSuffix(v Variant) string {
	if v.Label == "" {
		return ""
	}
	return ", " + v.Label
}

func variantList(s Service) string {
	var ids []string
	for _, v := range s.Variants {
		ids = append(ids, fmt.Sprintf("%d (%dmin%s)", v.ID, v.DurationMin, labelSuffix(v)))
	}
	return strings.Join(ids, ", ")
}
