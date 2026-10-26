// Package cookies reads a browser cookie export and pulls out the two values
// the CLI needs from booksy.com: the identity-{country} access token and the
// _bf device fingerprint. Only booksy.com cookies are considered, so a
// whole-browser export is fine.
package cookies

import (
	"bufio"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Cookie struct {
	Domain  string
	Name    string
	Value   string
	Expires time.Time
}

// Identity is what an export yields for one country.
type Identity struct {
	Country     string
	Token       string
	Fingerprint string
	Expires     time.Time
}

// Format names an export layout. Auto detects it.
type Format string

const (
	Auto     Format = "auto"
	JSON     Format = "json"
	Netscape Format = "netscape"
	Header   Format = "header"
)

var identityName = regexp.MustCompile(`^identity-([a-z]{2})$`)

// bareToken matches the 32-character token as pasted on its own.
var bareToken = regexp.MustCompile(`^[A-Za-z0-9]{24,64}$`)

// Parse reads raw in the given format and returns cookies for booksy.com.
func Parse(raw string, f Format) ([]Cookie, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if raw == "" {
		return nil, errors.New("the cookie export is empty")
	}
	if f == Auto || f == "" {
		f = detect(raw)
	}
	var all []Cookie
	var err error
	switch f {
	case JSON:
		all, err = parseJSON(raw)
	case Netscape:
		all = parseNetscape(raw)
	case Header:
		all = parseHeader(raw)
	default:
		return nil, errors.New("unknown cookie format " + string(f) + " (want auto, json, netscape or header)")
	}
	if err != nil {
		return nil, err
	}
	var out []Cookie
	for _, c := range all {
		if isBooksy(c.Domain) {
			out = append(out, c)
		}
	}
	return out, nil
}

func detect(raw string) Format {
	switch {
	case strings.HasPrefix(raw, "[") || strings.HasPrefix(raw, "{"):
		return JSON
	case strings.Contains(raw, "\t") || strings.HasPrefix(raw, "# Netscape") || strings.HasPrefix(raw, "# HTTP Cookie File"):
		return Netscape
	}
	return Header
}

// isBooksy accepts booksy.com and its subdomains; an empty domain (a pasted
// Cookie header) is trusted to be from booksy since the user chose to paste it.
func isBooksy(domain string) bool {
	d := strings.TrimPrefix(strings.ToLower(domain), ".")
	return d == "" || d == "booksy.com" || strings.HasSuffix(d, ".booksy.com")
}

type jsonCookie struct {
	Domain         string   `json:"domain"`
	Name           string   `json:"name"`
	Value          string   `json:"value"`
	ExpirationDate *float64 `json:"expirationDate"`
	Expires        *float64 `json:"expires"`
}

func parseJSON(raw string) ([]Cookie, error) {
	var list []jsonCookie
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		var wrap struct {
			URL     string       `json:"url"`
			Cookies []jsonCookie `json:"cookies"`
		}
		if err2 := json.Unmarshal([]byte(raw), &wrap); err2 != nil {
			return nil, errors.New("the cookie export is not a JSON cookie list: " + err.Error())
		}
		list = wrap.Cookies
		for i := range list {
			if list[i].Domain == "" && strings.Contains(wrap.URL, "booksy.com") {
				list[i].Domain = "booksy.com"
			}
		}
	}
	out := make([]Cookie, 0, len(list))
	for _, j := range list {
		c := Cookie{Domain: j.Domain, Name: j.Name, Value: j.Value}
		exp := j.ExpirationDate
		if exp == nil {
			exp = j.Expires
		}
		if exp != nil && *exp > 0 {
			c.Expires = time.Unix(int64(*exp), 0).UTC()
		}
		out = append(out, c)
	}
	return out, nil
}

func parseNetscape(raw string) []Cookie {
	var out []Cookie
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		c := Cookie{Domain: f[0], Name: f[5], Value: f[6]}
		if n, err := strconv.ParseInt(f[4], 10, 64); err == nil && n > 0 {
			c.Expires = time.Unix(n, 0).UTC()
		}
		out = append(out, c)
	}
	return out
}

func parseHeader(raw string) []Cookie {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(strings.ToLower(raw), "cookie:"); i == 0 {
		raw = raw[len("cookie:"):]
	}
	if !strings.Contains(raw, "=") {
		if bareToken.MatchString(raw) {
			return []Cookie{{Name: "identity-", Value: raw}}
		}
		return nil
	}
	var out []Cookie
	for _, part := range strings.Split(raw, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			out = append(out, Cookie{Name: name, Value: value})
		}
	}
	return out
}

// Identities returns one Identity per identity-{country} cookie. A bare
// pasted token comes back with an empty Country for the caller to fill.
func Identities(cs []Cookie) []Identity {
	fp := ""
	for _, c := range cs {
		if c.Name == "_bf" && c.Value != "" {
			fp = c.Value
		}
	}
	var out []Identity
	for _, c := range cs {
		if c.Value == "" {
			continue
		}
		if c.Name == "identity-" {
			out = append(out, Identity{Token: c.Value, Fingerprint: fp})
			continue
		}
		if m := identityName.FindStringSubmatch(c.Name); m != nil {
			out = append(out, Identity{Country: m[1], Token: c.Value, Fingerprint: fp, Expires: c.Expires})
		}
	}
	return out
}
