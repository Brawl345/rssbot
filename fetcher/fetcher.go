// Package fetcher performs well-behaved HTTP feed retrieval following the
// rachelbythebay Feed Reader Behavior project (FRB) and earth.org.uk
// RSS-efficiency recommendations: conditional GETs, identifying User-Agent,
// compression, server-hint parsing and manual redirect handling.
package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/rss"
)

const (
	defaultTimeout = 30 * time.Second
	maxRedirects   = 5
	maxBodyBytes   = 16 << 20 // 16 MiB
	bodySnippetLen = 500
	repoURL        = "https://github.com/Brawl345/rssbot"
)

type Fetcher struct {
	client      *http.Client
	userAgent   string
	privateHost func(ctx context.Context, host string) bool
}

// Result is the interpreted outcome of a single feed fetch. ETag and
// LastModified form an atomic set (FRB014) and are only populated from a
// status 200 response.
type Result struct {
	Status       int
	NotModified  bool          // 304: nothing changed since the conditional request
	Feed         *gofeed.Feed  // only set on 200 with a parseable feed
	ETag         string        // exactly as received (FRB003)
	LastModified string        // exactly as received (FRB001)
	MaxAge       time.Duration // Cache-Control: max-age (FRB022), 0 if absent
	RetryAfter   time.Duration // Retry-After on 429/503 (FRB020/021), 0 if absent
	FeedInterval time.Duration // ttl hint (FRB023/024), 0 if absent
	SkipHours    []int         // RSS skipHours (FRB024)
	SkipDays     []string      // RSS skipDays (FRB024)
	PermanentURL string        // set on a 301/308 chain ending in 200/304 (FRB130/131)
	Body         string        // snippet of an error body for the user (FRB101/120)
}

func New() *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: defaultTimeout,
			// Handle redirects manually so permanent (301/308) moves can be
			// persisted while temporary (302/307) ones are merely followed.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		userAgent:   buildUserAgent(),
		privateHost: isPrivateHost,
	}
}

// UserAgent returns the identifying User-Agent string sent with every request.
func (f *Fetcher) UserAgent() string { return f.userAgent }

// Fetch issues a single conditional GET for feedURL. etag and lastModified, if
// non-empty, are sent back verbatim as If-None-Match / If-Modified-Since.
func (f *Fetcher) Fetch(ctx context.Context, feedURL, etag, lastModified string) (*Result, error) {
	currentURL := feedURL
	permanentPrefix := true
	var permanentURL string
	// A feed the admin subscribed to on an internal address may redirect
	// internally, but a public feed must not bounce the bot into the local
	// network (SSRF). Only resolved once a redirect is actually followed.
	var originPrivate *bool

	for hop := 0; ; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", f.userAgent)
		// The stored validators belong to the final resource of the chain, so
		// they are sent on every hop; otherwise feeds behind a temporary
		// redirect could never be answered with 304.
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		if lastModified != "" {
			req.Header.Set("If-Modified-Since", lastModified)
		}

		resp, err := f.client.Do(req)
		if err != nil {
			return nil, err
		}

		if isRedirect(resp.StatusCode) {
			location := resp.Header.Get("Location")
			// Drain a little so the connection can be reused for the next hop.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
			_ = resp.Body.Close()
			if location == "" {
				return nil, fmt.Errorf("redirect status %d without Location header", resp.StatusCode)
			}
			if hop >= maxRedirects {
				return nil, fmt.Errorf("too many redirects (>%d)", maxRedirects)
			}

			next, err := resolveLocation(currentURL, location)
			if err != nil {
				return nil, err
			}
			if originPrivate == nil {
				private := f.privateHost(ctx, hostOf(feedURL))
				originPrivate = &private
			}
			if !*originPrivate && f.privateHost(ctx, hostOf(next)) {
				return nil, fmt.Errorf("redirect to private address %s refused", next)
			}
			if (resp.StatusCode == http.StatusMovedPermanently ||
				resp.StatusCode == http.StatusPermanentRedirect) && permanentPrefix {
				permanentURL = next
			} else {
				permanentPrefix = false
			}
			currentURL = next
			continue
		}

		result := &Result{Status: resp.StatusCode}
		// Only report a move when the new location actually serves the feed.
		succeeded := resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotModified
		if succeeded && permanentURL != "" && permanentURL != feedURL {
			result.PermanentURL = permanentURL
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}

		switch resp.StatusCode {
		case http.StatusNotModified:
			result.NotModified = true
			result.MaxAge = parseMaxAge(resp.Header.Get("Cache-Control"))
		case http.StatusOK:
			feed, err := gofeed.NewParser().Parse(bytes.NewReader(body))
			if err != nil {
				result.Body = snippet(body)
				return result, fmt.Errorf("not a valid feed: %w", err)
			}
			result.Feed = feed
			result.ETag = resp.Header.Get("Etag")
			result.LastModified = resp.Header.Get("Last-Modified")
			result.MaxAge = parseMaxAge(resp.Header.Get("Cache-Control"))
			applyFeedHints(result, body)
		case http.StatusTooManyRequests, http.StatusServiceUnavailable:
			result.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
			result.Body = snippet(body)
		default:
			result.Body = snippet(body)
		}

		return result, nil
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func resolveLocation(base, location string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	l, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	next := b.ResolveReference(l)
	if next.Scheme != "http" && next.Scheme != "https" {
		return "", fmt.Errorf("redirect to unsupported scheme %q", next.Scheme)
	}
	return next.String(), nil
}

func hostOf(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		return u.Hostname()
	}
	return ""
}

// isPrivateHost reports whether host is or resolves to a loopback, private,
// link-local or unspecified address.
func isPrivateHost(ctx context.Context, host string) bool {
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIP(ip)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if isPrivateIP(addr.IP) {
			return true
		}
	}
	return false
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func parseMaxAge(cacheControl string) time.Duration {
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	return 0
}

// parseRetryAfter accepts either delta-seconds or an HTTP-date (RFC 7231).
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// applyFeedHints extracts RSS polling hints (ttl, skipHours, skipDays) from the
// raw body when it is an RSS feed. sy:updatePeriod is deliberately ignored:
// WordPress emits "hourly" by default for every feed, regardless of how often it
// actually changes.
func applyFeedHints(result *Result, body []byte) {
	feedType := gofeed.DetectFeedType(bytes.NewReader(body))
	if feedType != gofeed.FeedTypeRSS {
		return
	}
	rssFeed, err := (&rss.Parser{}).Parse(bytes.NewReader(body))
	if err != nil {
		return
	}

	for _, h := range rssFeed.SkipHours {
		if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n >= 0 && n <= 23 {
			result.SkipHours = append(result.SkipHours, n)
		}
	}
	result.SkipDays = rssFeed.SkipDays

	if ttl := strings.TrimSpace(rssFeed.TTL); ttl != "" {
		if mins, err := strconv.Atoi(ttl); err == nil && mins > 0 {
			result.FeedInterval = time.Duration(mins) * time.Minute
		}
	}
}

func snippet(body []byte) string {
	s := strings.TrimSpace(strings.ToValidUTF8(string(body), ""))
	if utf8.RuneCountInString(s) > bodySnippetLen {
		return string([]rune(s)[:bodySnippetLen]) + "…"
	}
	return s
}

// Version can be set at build time via
// -ldflags "-X github.com/Brawl345/rssbot/fetcher.Version=...". Otherwise the
// VCS revision embedded by the Go toolchain is used.
var Version string

func buildUserAgent() string {
	if Version != "" {
		return fmt.Sprintf("rssbot/%s (+%s)", Version, repoURL)
	}
	version := "dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				version = setting.Value
				if len(version) > 7 {
					version = version[:7]
				}
				break
			}
		}
	}
	return fmt.Sprintf("rssbot/%s (+%s)", version, repoURL)
}
