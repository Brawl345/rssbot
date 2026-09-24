package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleRSS = `<?xml version="1.0"?>
<rss version="2.0" xmlns:sy="http://purl.org/rss/1.0/modules/syndication/">
<channel>
  <title>Test</title>
  <link>https://example.org</link>
  <ttl>90</ttl>
  <skipHours><hour>0</hour><hour>23</hour></skipHours>
  <skipDays><day>Sunday</day></skipDays>
  <sy:updatePeriod>hourly</sy:updatePeriod>
  <sy:updateFrequency>2</sy:updateFrequency>
  <item><title>Item 1</title><link>https://example.org/1</link><guid>1</guid></item>
</channel>
</rss>`

func fetch(t *testing.T, f *Fetcher, url, etag, lm string) *Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := f.Fetch(ctx, url, etag, lm)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return res
}

func TestFetchSetsHeadersAndParses(t *testing.T) {
	var gotMethod, gotUA, gotINM, gotIMS, gotAE string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotUA = r.Header.Get("User-Agent")
		gotINM = r.Header.Get("If-None-Match")
		gotIMS = r.Header.Get("If-Modified-Since")
		gotAE = r.Header.Get("Accept-Encoding")
		w.Header().Set("Etag", `"abc"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Cache-Control", "max-age=1800")
		w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()

	f := New()
	res := fetch(t, f, srv.URL, "", "")

	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET (FRB050)", gotMethod)
	}
	if !strings.HasPrefix(gotUA, "rssbot/") || !strings.Contains(gotUA, "github.com/Brawl345/rssbot") {
		t.Errorf("User-Agent = %q, want identifying rssbot UA (FRB080-090)", gotUA)
	}
	if gotINM != "" || gotIMS != "" {
		t.Errorf("first request must be unconditional, got INM=%q IMS=%q (FRB012/013)", gotINM, gotIMS)
	}
	if !strings.Contains(gotAE, "gzip") {
		t.Errorf("Accept-Encoding = %q, want gzip (FRB141)", gotAE)
	}
	if res.Status != 200 || res.Feed == nil || len(res.Feed.Items) != 1 {
		t.Fatalf("unexpected result: status=%d feed=%v", res.Status, res.Feed)
	}
	if res.ETag != `"abc"` {
		t.Errorf("ETag = %q, want %q stored verbatim (FRB003)", res.ETag, `"abc"`)
	}
	if res.LastModified != "Wed, 21 Oct 2015 07:28:00 GMT" {
		t.Errorf("LastModified = %q, not stored verbatim (FRB001)", res.LastModified)
	}
	if res.MaxAge != 30*time.Minute {
		t.Errorf("MaxAge = %s, want 30m (FRB022)", res.MaxAge)
	}
	if res.FeedInterval != 90*time.Minute {
		t.Errorf("FeedInterval = %s, want 90m from ttl (FRB024)", res.FeedInterval)
	}
	if len(res.SkipHours) != 2 || res.SkipHours[0] != 0 || res.SkipHours[1] != 23 {
		t.Errorf("SkipHours = %v, want [0 23] (FRB024)", res.SkipHours)
	}
	if len(res.SkipDays) != 1 || res.SkipDays[0] != "Sunday" {
		t.Errorf("SkipDays = %v, want [Sunday] (FRB024)", res.SkipDays)
	}
}

func TestSyndicationHintIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Replace(sampleRSS, "<ttl>90</ttl>", "", 1)))
	}))
	defer srv.Close()

	if res := fetch(t, New(), srv.URL, "", ""); res.FeedInterval != 0 {
		t.Errorf("FeedInterval = %s, want 0 without ttl", res.FeedInterval)
	}
}

func TestConditionalRequestEchoed(t *testing.T) {
	var gotINM, gotIMS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		gotIMS = r.Header.Get("If-Modified-Since")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	f := New()
	res := fetch(t, f, srv.URL, `"abc"`, "Wed, 21 Oct 2015 07:28:00 GMT")

	if gotINM != `"abc"` {
		t.Errorf("If-None-Match = %q, want verbatim etag (FRB004/013)", gotINM)
	}
	if gotIMS != "Wed, 21 Oct 2015 07:28:00 GMT" {
		t.Errorf("If-Modified-Since = %q, want verbatim last-modified (FRB002/012)", gotIMS)
	}
	if !res.NotModified || res.Status != http.StatusNotModified {
		t.Errorf("expected 304 NotModified, got status=%d notmod=%v", res.Status, res.NotModified)
	}
}

func TestConditionalRequestThroughTemporaryRedirect(t *testing.T) {
	var gotINM string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer target.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer src.Close()

	res := fetch(t, New(), src.URL, `"abc"`, "")
	if gotINM != `"abc"` {
		t.Errorf("If-None-Match after 302 = %q, want %q", gotINM, `"abc"`)
	}
	if !res.NotModified {
		t.Errorf("expected 304 through temporary redirect, got status=%d", res.Status)
	}
}

func TestRetryAfterParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("slow down"))
	}))
	defer srv.Close()

	res := fetch(t, New(), srv.URL, "", "")
	if res.Status != 429 {
		t.Fatalf("status = %d, want 429", res.Status)
	}
	if res.RetryAfter != 2*time.Minute {
		t.Errorf("RetryAfter = %s, want 2m (FRB020)", res.RetryAfter)
	}
	if res.Body != "slow down" {
		t.Errorf("Body = %q, want error snippet (FRB120)", res.Body)
	}
}

func TestPermanentRedirectReported(t *testing.T) {
	var target *httptest.Server
	target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRSS))
	}))
	defer target.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusMovedPermanently)
	}))
	defer src.Close()

	res := fetch(t, New(), src.URL, "", "")
	if res.PermanentURL != target.URL {
		t.Errorf("PermanentURL = %q, want %q (FRB130)", res.PermanentURL, target.URL)
	}
	if res.Status != 200 || res.Feed == nil {
		t.Errorf("expected feed content after following 301, got status=%d", res.Status)
	}
}

func TestPermanentRedirectToErrorNotReported(t *testing.T) {
	target := httptest.NewServer(http.NotFoundHandler())
	defer target.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusMovedPermanently)
	}))
	defer src.Close()

	res := fetch(t, New(), src.URL, "", "")
	if res.PermanentURL != "" {
		t.Errorf("PermanentURL = %q, want empty when target fails", res.PermanentURL)
	}
	if res.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.Status)
	}
}

func TestTemporaryRedirectNotPersisted(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRSS))
	}))
	defer target.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound) // 302
	}))
	defer src.Close()

	res := fetch(t, New(), src.URL, "", "")
	if res.PermanentURL != "" {
		t.Errorf("PermanentURL = %q, want empty for 302 (FRB133)", res.PermanentURL)
	}
	if res.Status != 200 || res.Feed == nil {
		t.Errorf("expected feed content after following 302, got status=%d", res.Status)
	}
}

func TestRedirectFromPublicToPrivateRefused(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://internal.test/feed", http.StatusFound)
	}))
	defer src.Close()

	f := New()
	f.privateHost = func(_ context.Context, host string) bool { return host == "internal.test" }

	_, err := f.Fetch(context.Background(), src.URL, "", "")
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("err = %v, want refused private redirect", err)
	}
}

func TestRedirectToUnsupportedSchemeRefused(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer src.Close()

	_, err := New().Fetch(context.Background(), src.URL, "", "")
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("err = %v, want refused scheme", err)
	}
}

func TestNonFeedRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>not a feed</body></html>"))
	}))
	defer srv.Close()

	ctx := context.Background()
	res, err := New().Fetch(ctx, srv.URL, "", "")
	if err == nil {
		t.Fatalf("expected error for non-feed body (FRB102)")
	}
	if res == nil || res.Body == "" {
		t.Errorf("expected body snippet on parse failure (FRB101)")
	}
}
