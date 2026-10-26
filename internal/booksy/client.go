// Package booksy is a client for Booksy's unofficial customer web API, the
// one booksy.com and its booking widget use. Every endpoint here was observed
// in the web bundles and probed live; none of it is documented by Booksy.
package booksy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// WebAPIKey is the public key booksy.com ships to every browser. It identifies
// the web client, not a user.
const WebAPIKey = "web-e3d812bf-d7a2-445d-ab38-55589ae6a121"

// appVersion is what the booking widget sends in X-App-Version and in draft meta.
const appVersion = "3.0"

const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// Client talks to https://{country}.booksy.com/core/v2. It holds no state
// besides configuration, so one per invocation is enough.
type Client struct {
	HTTP *http.Client
	// Country selects the regional host (es, us, pl, gb, ...).
	Country string
	// BaseURL overrides the regional host, for tests. No trailing slash.
	BaseURL string
	APIKey  string
	// AccessToken is the identity-{country} cookie value. Empty for public calls.
	AccessToken string
	// Fingerprint is a stable per-profile id. Booking drafts fail without one.
	Fingerprint string
	Language    string
	UserAgent   string
	// Debug, when set, receives one line per request with secrets redacted.
	Debug io.Writer
	// WebURL is booksy.com itself, where the web key is rediscovered. Tests
	// point it at a fake.
	WebURL string
	// OnKeyRejected, when set, is called once when Booksy refuses APIKey. It
	// returns a replacement (normally from DiscoverAPIKey) and the request is
	// retried with it.
	OnKeyRejected func(ctx context.Context) (string, error)
}

func New(country string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone(), CheckRedirect: sameOrigin},
		Country:   country,
		APIKey:    WebAPIKey,
		UserAgent: browserUA,
	}
}

// sameOrigin refuses any redirect that leaves the first request's scheme and
// host. Go re-sends custom headers such as X-Access-Token on every hop, so a
// redirect elsewhere (or to plain http) would hand the login to that host.
// The API itself never redirects; booksy.com only redirects within itself.
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	first := via[0].URL
	if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
		return fmt.Errorf("refusing redirect from %s to %s://%s", first.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// UseProxy routes every request through u. There is no direct fallback.
func (c *Client) UseProxy(u *url.URL) {
	t := c.HTTP.Transport.(*http.Transport)
	t.Proxy = http.ProxyURL(u)
}

// ParseProxy accepts http, https, socks5 and socks5h URLs.
func ParseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (want http, https, socks5 or socks5h)", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("proxy URL has no host")
	}
	return u, nil
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://" + c.Country + ".booksy.com"
}

// HasToken reports whether authenticated calls can be attempted.
func (c *Client) HasToken() bool { return c.AccessToken != "" }

func (c *Client) requireToken(endpoint string) error {
	if c.AccessToken == "" {
		return &Error{Kind: KindAuth, Endpoint: endpoint, Msg: "this needs a logged-in profile. Run `booksy auth login`"}
	}
	return nil
}

// call performs one request against /core/v2/customer_api (or another /core/v2
// path when path starts with /core). body is JSON-encoded; out is decoded from
// the JSON response unless nil.
func (c *Client) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if !strings.HasPrefix(path, "/core/") {
		path = "/core/v2/customer_api" + path
	}
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = raw
	}
	err := c.once(ctx, method, path, query, payload, body != nil, out)
	var e *Error
	if !errors.As(err, &e) || !e.KeyRejected || c.OnKeyRejected == nil {
		return err
	}
	// Recover at most once per client: a second refusal means the
	// rediscovered key is wrong too, and looping would only hammer booksy.
	hook := c.OnKeyRejected
	c.OnKeyRejected = nil
	key, derr := hook(ctx)
	if derr != nil || key == "" || key == c.APIKey {
		e.Msg += ". Rediscovering the current key from booksy.com did not help"
		if derr != nil {
			e.Msg += fmt.Sprintf(" (%v)", derr)
		}
		return e
	}
	c.debugf("web api key rejected; retrying with the one booksy.com serves now")
	c.APIKey = key
	return c.once(ctx, method, path, query, payload, body != nil, out)
}

func (c *Client) once(ctx context.Context, method, path string, query url.Values, payload []byte, hasBody bool, out any) error {
	endpoint := method + " " + templatePath(path)
	u := c.base() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if hasBody {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("X-App-Version", appVersion)
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Language != "" {
		req.Header.Set("Accept-Language", c.Language)
	}
	if c.AccessToken != "" {
		req.Header.Set("X-Access-Token", c.AccessToken)
	}
	if c.Fingerprint != "" {
		req.Header.Set("X-Fingerprint", c.Fingerprint)
	}

	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.debugf("%s %s -> error %v", method, redactURL(u), err)
		return &Error{Kind: KindNetwork, Endpoint: endpoint, Retryable: true, Msg: fmt.Sprintf("cannot reach booksy: %v", netCause(err))}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return &Error{Kind: KindNetwork, Endpoint: endpoint, Retryable: true, Msg: fmt.Sprintf("reading booksy response: %v", err)}
	}
	c.debugf("%s %s -> %d (%d bytes, %s)", method, redactURL(u), resp.StatusCode, len(raw), time.Since(start).Round(time.Millisecond))

	if resp.StatusCode >= 300 {
		return classify(resp.StatusCode, endpoint, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &Error{Kind: KindAPIChanged, Status: resp.StatusCode, Endpoint: endpoint, Body: excerpt(raw), Msg: fmt.Sprintf("unexpected response from %s: %v", endpoint, err)}
	}
	return nil
}

// apiErrors is Booksy's error envelope: {"errors":[{code, description, field}]}.
type apiErrors struct {
	Errors []struct {
		Code        string `json:"code"`
		Description string `json:"description"`
		Field       string `json:"field"`
	} `json:"errors"`
}

func classify(status int, endpoint string, raw []byte) error {
	e := &Error{Status: status, Endpoint: endpoint, Body: excerpt(raw)}
	var env apiErrors
	isJSON := json.Unmarshal(raw, &env) == nil
	desc := ""
	if isJSON && len(env.Errors) > 0 && env.Errors[0].Field == "api_key" {
		// The public web key, not the user's login. Exit 7 unless the caller
		// recovers by rediscovering it.
		e.Kind, e.KeyRejected, e.APICode = KindAPIChanged, true, env.Errors[0].Code
		e.Msg = "booksy rejected the web api key the CLI sends (" + env.Errors[0].Description + "). booksy.com may have changed it"
		return e
	}
	if isJSON && len(env.Errors) > 0 {
		e.APICode = env.Errors[0].Code
		desc = env.Errors[0].Description
		if f := env.Errors[0].Field; f != "" && f != "auth_header_token" {
			desc = f + ": " + desc
		}
	}
	switch {
	case status == http.StatusTooManyRequests:
		e.Kind, e.Retryable = KindBlocked, true
		e.Msg = "booksy is rate limiting this address (HTTP 429). Wait a few minutes"
	case (status == 401 || status == 403) && isJSON:
		e.Kind = KindAuth
		e.Msg = "booksy rejected the access token"
		if desc != "" {
			e.Msg += ": " + desc
		}
	case status == 403:
		e.Kind, e.Retryable = KindBlocked, true
		e.Msg = "booksy blocked the request (HTTP 403, not an API answer). Wait, or use a proxy"
	case status == 404:
		e.Kind = KindNotFound
		e.Msg = "not found on booksy"
	case status == 405:
		e.Kind = KindAPIChanged
		e.Msg = fmt.Sprintf("booksy no longer accepts %s", endpoint)
	case status >= 400 && status < 500 && isJSON:
		e.Kind = KindUsage
		e.Msg = "booksy refused the request"
		if desc != "" {
			e.Msg += ": " + desc
		}
	case status >= 500:
		e.Kind, e.Retryable = KindGeneric, true
		e.Msg = fmt.Sprintf("booksy failed with HTTP %d", status)
		if desc != "" {
			e.Msg += ": " + desc
		}
	default:
		e.Kind = KindAPIChanged
		e.Msg = fmt.Sprintf("unexpected HTTP %d from %s", status, endpoint)
	}
	return e
}

var numSegment = regexp.MustCompile(`/[0-9a-f-]{6,}|/\d+`)

// templatePath turns /businesses/3349/reviews into /businesses/{id}/reviews so
// endpoints in errors and issue links never carry ids.
func templatePath(p string) string {
	return numSegment.ReplaceAllString(p, "/{id}")
}

func redactURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		q, err := url.ParseQuery(u[i+1:])
		if err == nil {
			for k := range q {
				if strings.Contains(strings.ToLower(k), "token") {
					q.Set(k, "REDACTED")
				}
			}
			return u[:i+1] + q.Encode()
		}
	}
	return u
}

var tokenish = regexp.MustCompile(`("(?:access_token|token|email|cell_phone|first_name|last_name)"\s*:\s*)"[^"]*"`)

// excerpt keeps the first 400 bytes with obvious personal fields blanked.
func excerpt(raw []byte) string {
	s := tokenish.ReplaceAllString(string(raw), `$1"REDACTED"`)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func netCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		return oe.Err
	}
	return err
}

func (c *Client) debugf(format string, args ...any) {
	if c.Debug != nil {
		fmt.Fprintf(c.Debug, "booksy: debug: "+format+"\n", args...)
	}
}
