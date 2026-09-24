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
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

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
	client    *http.Client
	userAgent string
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
	PermanentURL string        // set on a 301/308 chain (FRB130/131)
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
		userAgent: buildUserAgent(),
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
			resp.Body.Close()
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
		if permanentURL != "" && permanentURL != feedURL {
			result.PermanentURL = permanentURL
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		resp.Body.Close()
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
	return b.ResolveReference(l).String(), nil
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
	s := strings.TrimSpace(string(body))
	if len(s) > bodySnippetLen {
		return s[:bodySnippetLen] + "…"
	}
	return s
}

func buildUserAgent() string {
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
