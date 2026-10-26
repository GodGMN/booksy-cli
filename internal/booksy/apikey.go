package booksy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// The web key is not configuration Booksy publishes; it is a constant in the
// booksy.com bundle, next to its development twin:
//
//	hn={DEVELOPMENT:`web-af3c…`,PRODUCTION:`web-e3d8…`}
//
// DiscoverAPIKey finds it the way a browser would: load the home page, then
// its scripts, then the chunks those scripts import, until one carries it.

var (
	keyPattern    = regexp.MustCompile("PRODUCTION:\\s*[`\"'](web-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})[`\"']")
	scriptURL     = regexp.MustCompile(`(?:src|href)="([^"]+\.js)"`)
	assetURL      = regexp.MustCompile(`https?://[^"'\s()]+?\.js`)
	chunkRef      = regexp.MustCompile("[`\"'](?:\\./)?([A-Za-z0-9_-]{4,}\\.js)[`\"']")
	errKeyMissing = errors.New("no web api key in the booksy.com scripts")
)

// maxScripts bounds discovery: the marketplace loads a few hundred chunks.
const maxScripts = 600

func (c *Client) webURL() string {
	if c.WebURL != "" {
		return strings.TrimRight(c.WebURL, "/")
	}
	return "https://booksy.com"
}

func (c *Client) fetchText(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%s answered HTTP %d", req.URL.Host, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(raw), err
}

// DiscoverAPIKey returns the production web key booksy.com serves right now.
func (c *Client) DiscoverAPIKey(ctx context.Context) (string, error) {
	home := c.webURL() + "/" + Locale(c.Country) + "/"
	page, err := c.fetchText(ctx, home)
	if err != nil {
		return "", fmt.Errorf("loading %s: %w", home, err)
	}
	if m := keyPattern.FindStringSubmatch(page); m != nil {
		return m[1], nil
	}
	base, _ := url.Parse(home)
	seen := map[string]bool{}
	var queue []string
	push := func(ref string, from *url.URL) {
		u, err := from.Parse(ref)
		if err != nil {
			return
		}
		s := u.String()
		if !seen[s] && len(seen) < maxScripts {
			seen[s] = true
			queue = append(queue, s)
		}
	}
	for _, m := range scriptURL.FindAllStringSubmatch(page, -1) {
		push(m[1], base)
	}
	for _, m := range assetURL.FindAllString(page, -1) {
		push(m, base)
	}

	for len(queue) > 0 {
		batch := queue
		queue = nil
		found := ""
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		texts := make([]string, len(batch))
		for i, u := range batch {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				js, err := c.fetchText(ctx, u)
				if err != nil {
					return
				}
				texts[i] = js
				if m := keyPattern.FindStringSubmatch(js); m != nil {
					mu.Lock()
					found = m[1]
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if found != "" {
			c.debugf("found the web api key after %d scripts", len(seen))
			return found, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// Next level: chunks imported by this level, resolved next to the importer.
		for i, js := range texts {
			from, err := url.Parse(batch[i])
			if err != nil {
				continue
			}
			for _, m := range chunkRef.FindAllStringSubmatch(js, -1) {
				push(m[1], from)
			}
		}
	}
	return "", errKeyMissing
}
