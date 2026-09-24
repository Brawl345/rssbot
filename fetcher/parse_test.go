package fetcher

import (
	"compress/gzip"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseMaxAge(t *testing.T) {
	tests := map[string]time.Duration{
		"":                                  0,
		"max-age=600":                       10 * time.Minute,
		"public, max-age=3600, s-maxage=60": time.Hour,
		"no-cache":                          0,
		"max-age=0":                         0,
		"max-age=-5":                        0,
		"max-age=abc":                       0,
	}
	for in, want := range tests {
		if got := parseMaxAge(in); got != want {
			t.Errorf("parseMaxAge(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := map[string]time.Duration{
		"":                              0,
		"120":                           2 * time.Minute,
		" 30 ":                          30 * time.Second,
		"-1":                            0,
		"garbage":                       0,
		"Wed, 21 Oct 2015 07:28:00 GMT": 0,
	}
	for in, want := range tests {
		if got := parseRetryAfter(in); got != want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", in, got, want)
		}
	}

	future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got < 59*time.Minute || got > time.Hour {
		t.Errorf("parseRetryAfter(%q) = %s, want ~1h", future, got)
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1":       true,
		"10.1.2.3":        true,
		"192.168.0.1":     true,
		"172.16.0.1":      true,
		"169.254.169.254": true,
		"0.0.0.0":         true,
		"::1":             true,
		"fe80::1":         true,
		"fd00::1":         true,
		"1.1.1.1":         false,
		"2606:4700::1111": false,
	}
	for in, want := range tests {
		if got := isPrivateIP(net.ParseIP(in)); got != want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestIsPrivateHostLiteral(t *testing.T) {
	ctx := context.Background()
	if !isPrivateHost(ctx, "127.0.0.1") || isPrivateHost(ctx, "8.8.8.8") || isPrivateHost(ctx, "") {
		t.Errorf("isPrivateHost misclassified an IP literal")
	}
}

func TestTooManyRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()

	_, err := New().Fetch(context.Background(), srv.URL+"/", "", "")
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("err = %v, want too many redirects", err)
	}
}

func TestRedirectWithoutLocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()

	if _, err := New().Fetch(context.Background(), srv.URL, "", ""); err == nil {
		t.Fatal("expected error for redirect without Location")
	}
}

func TestPermanentChainBrokenByTemporaryRedirect(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleRSS))
	}))
	defer final.Close()
	temp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusTemporaryRedirect)
	}))
	defer temp.Close()
	perm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, temp.URL, http.StatusPermanentRedirect)
	}))
	defer perm.Close()

	res := fetch(t, New(), perm.URL, "", "")
	if res.PermanentURL != temp.URL {
		t.Errorf("PermanentURL = %q, want the last permanent target %q", res.PermanentURL, temp.URL)
	}
}

func TestGzipResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			http.Error(w, "gzip expected", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(sampleRSS))
		_ = gz.Close()
	}))
	defer srv.Close()

	res := fetch(t, New(), srv.URL, "", "")
	if res.Feed == nil || len(res.Feed.Items) != 1 {
		t.Fatalf("gzip feed not decoded: status=%d", res.Status)
	}
}

func TestErrorStatusKeepsBodySnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "maintenance", http.StatusInternalServerError)
	}))
	defer srv.Close()

	res := fetch(t, New(), srv.URL, "", "")
	if res.Status != http.StatusInternalServerError || res.Body != "maintenance" || res.Feed != nil {
		t.Errorf("unexpected result: status=%d body=%q", res.Status, res.Body)
	}
}

func TestUserAgentVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "abc1234"
	if ua := buildUserAgent(); ua != "rssbot/abc1234 (+https://github.com/Brawl345/rssbot)" {
		t.Errorf("User-Agent = %q", ua)
	}
}
