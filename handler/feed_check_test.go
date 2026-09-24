package handler

import (
	"database/sql"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Brawl345/rssbot/config"
	"github.com/Brawl345/rssbot/fetcher"
	"github.com/Brawl345/rssbot/storage"
	"github.com/mmcdole/gofeed"
)

func testPollConfig() config.PollConfig {
	return config.PollConfig{
		Interval:    10 * time.Minute,
		IntervalMax: 6 * time.Hour,
		Adaptive:    true,
		Concurrency: 4,
		Tick:        time.Second,
	}
}

func handlerWithPoll(poll config.PollConfig) *Handler {
	return &Handler{Config: &config.Config{Poll: poll}}
}

func assertDelay(t *testing.T, next time.Time, want time.Duration) {
	t.Helper()
	got := time.Until(next)
	if got < want-time.Second || got > want+time.Second {
		t.Errorf("next poll in %s, want %s", got.Round(time.Second), want)
	}
}

func TestNextPollAdaptive(t *testing.T) {
	h := handlerWithPoll(testPollConfig())

	tests := []struct {
		unchanged int
		want      time.Duration
	}{
		{0, 10 * time.Minute},
		{1, 20 * time.Minute},
		{2, 30 * time.Minute},
		{35, 6 * time.Hour},
		{36, 6 * time.Hour},
		{1 << 30, 6 * time.Hour},
	}
	for _, tt := range tests {
		assertDelay(t, h.nextPoll(tt.unchanged, nil), tt.want)
	}
}

func TestNextPollNotAdaptive(t *testing.T) {
	poll := testPollConfig()
	poll.Adaptive = false
	assertDelay(t, handlerWithPoll(poll).nextPoll(50, nil), 10*time.Minute)
}

func TestNextPollHints(t *testing.T) {
	h := handlerWithPoll(testPollConfig())

	tests := []struct {
		name   string
		result fetcher.Result
		want   time.Duration
	}{
		{"max-age slows down", fetcher.Result{MaxAge: time.Hour}, time.Hour},
		{"ttl slows down", fetcher.Result{FeedInterval: 90 * time.Minute}, 90 * time.Minute},
		{"larger hint wins", fetcher.Result{MaxAge: time.Hour, FeedInterval: 2 * time.Hour}, 2 * time.Hour},
		{"hint never speeds up", fetcher.Result{MaxAge: time.Minute}, 10 * time.Minute},
		{"hint capped", fetcher.Result{MaxAge: 365 * 24 * time.Hour}, 6 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDelay(t, h.nextPoll(0, &tt.result), tt.want)
		})
	}
}

func TestErrorBackoff(t *testing.T) {
	h := handlerWithPoll(testPollConfig())

	tests := []struct {
		errors int
		want   time.Duration
	}{
		{1, 10 * time.Minute},
		{2, 20 * time.Minute},
		{3, 40 * time.Minute},
		{6, 320 * time.Minute},
		{7, 6 * time.Hour},
		{100, 6 * time.Hour},
	}
	for _, tt := range tests {
		if got := h.errorBackoff(tt.errors); got != tt.want {
			t.Errorf("errorBackoff(%d) = %s, want %s", tt.errors, got, tt.want)
		}
	}
}

func TestApplySkip(t *testing.T) {
	// 2026-01-04 is a Sunday.
	at := func(day, hour int) time.Time { return time.Date(2026, 1, day, hour, 30, 0, 0, time.UTC) }

	tests := []struct {
		name  string
		t     time.Time
		hours []int
		days  []string
		want  time.Time
	}{
		{"no hints", at(4, 23), nil, nil, at(4, 23)},
		{"outside skipHours", at(4, 22), []int{23, 0}, nil, at(4, 22)},
		{"inside skipHours", at(4, 23), []int{23, 0}, nil, at(5, 1)},
		{"skipDays", at(4, 10), nil, []string{"Sunday"}, time.Date(2026, 1, 5, 0, 30, 0, 0, time.UTC)},
		{"skipDays is case-insensitive", at(4, 10), nil, []string{" sunday "}, time.Date(2026, 1, 5, 0, 30, 0, 0, time.UTC)},
		{"everything skipped is capped", at(4, 10), allHours(), nil, at(6, 10)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applySkip(tt.t, tt.hours, tt.days); !got.Equal(tt.want) {
				t.Errorf("applySkip = %s, want %s", got, tt.want)
			}
		})
	}
}

func allHours() []int {
	hours := make([]int, 24)
	for i := range hours {
		hours[i] = i
	}
	return hours
}

func TestFilterNewItems(t *testing.T) {
	items := []*gofeed.Item{
		{GUID: "3", Link: "https://example.org/3"},
		{GUID: "", Link: "https://example.org/2"},
		{GUID: "1", Link: "https://example.org/1"},
	}
	ptr := func(s string) *string { return &s }

	tests := []struct {
		name      string
		lastEntry *string
		want      int
	}{
		{"first poll", nil, 3},
		{"match by GUID", ptr("1"), 2},
		{"match by link", ptr("https://example.org/2"), 1},
		{"newest already seen", ptr("3"), 0},
		{"unknown entry returns all", ptr("gone"), 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterNewItems(items, tt.lastEntry); len(got) != tt.want {
				t.Errorf("got %d items, want %d", len(got), tt.want)
			}
		})
	}
}

func TestReverse(t *testing.T) {
	items := []*gofeed.Item{{GUID: "a"}, {GUID: "b"}, {GUID: "c"}}
	got := reverse(items)
	if got[0].GUID != "c" || got[2].GUID != "a" {
		t.Errorf("reverse = %v", []string{got[0].GUID, got[1].GUID, got[2].GUID})
	}
	if items[0].GUID != "a" {
		t.Errorf("reverse modified its input")
	}
}

func TestFeedHost(t *testing.T) {
	tests := map[string]string{
		"https://Example.org/feed.xml":  "example.org",
		"http://example.org:8080/rss":   "example.org:8080",
		"not a url":                     "not a url",
		"https://www.example.org/a?b=c": "www.example.org",
	}
	for in, want := range tests {
		if got := feedHost(in); got != want {
			t.Errorf("feedHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFeedURL(t *testing.T) {
	valid := map[string]string{
		"https://example.org/feed":      "https://example.org/feed",
		"  http://example.org/rss.xml ": "http://example.org/rss.xml",
		"https://example.org/?a=1&b=2":  "https://example.org/?a=1&b=2",
	}
	for in, want := range valid {
		got, err := sanitizeFeedURL(in)
		if err != nil || got != want {
			t.Errorf("sanitizeFeedURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	invalid := []string{
		"",
		"   ",
		"ftp://example.org/feed",
		"example.org/feed",
		"https:///feed",
		"https://example.org/a b",
		`https://example.org/"feed"`,
		"https://example.org/<feed>",
		"javascript:alert(1)",
	}
	for _, in := range invalid {
		if got, err := sanitizeFeedURL(in); err == nil {
			t.Errorf("sanitizeFeedURL(%q) = %q, want error", in, got)
		}
	}
}

func TestCompileReplacementsSkipsInvalidRegex(t *testing.T) {
	compiled := compileReplacements([]storage.Replacement{
		{Value: "[mehr]"},
		{Value: "([", IsRegex: true},
		{Value: "<.*?>", IsRegex: true},
	})
	if len(compiled) != 2 {
		t.Fatalf("got %d replacements, want 2", len(compiled))
	}
}

func TestProcessContent(t *testing.T) {
	replacements := compileReplacements([]storage.Replacement{
		{Value: "[mehr]"},
		{Value: "<.*?>", IsRegex: true},
	})

	got := processContent("<p>Hallo &amp; Tschüss [mehr]</p>\n\n   \n<p>Zeile 2</p>", replacements)
	if want := "Hallo & Tschüss \nZeile 2"; got != want {
		t.Errorf("processContent = %q, want %q", got, want)
	}
}

func TestProcessContentKeepsValidUTF8(t *testing.T) {
	out := processContent(strings.Repeat("ü", 300)+"\xff", nil)
	if !utf8.ValidString(out) {
		t.Fatalf("output is not valid UTF-8")
	}
	if !strings.HasSuffix(out, "...") || utf8.RuneCountInString(out) != 273 {
		t.Errorf("unexpected truncation: %d runes", utf8.RuneCountInString(out))
	}
}

func TestFeedStatus(t *testing.T) {
	if got := feedStatus(storage.Feed{}); got != "" {
		t.Errorf("active feed status = %q, want empty", got)
	}
	disabled := storage.Feed{Disabled: true, DisabledReason: sql.NullString{String: "HTTP <410>", Valid: true}}
	if got := feedStatus(disabled); !strings.Contains(got, "HTTP &lt;410&gt;") {
		t.Errorf("disabled feed status = %q, want escaped reason", got)
	}
}
