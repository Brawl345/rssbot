package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/Brawl345/rssbot/config"
	"github.com/Brawl345/rssbot/fetcher"
	"github.com/Brawl345/rssbot/storage"
	"gopkg.in/telebot.v3"
)

const adminID = 999

type sentMessage struct {
	ChatID int64
	Text   string
}

// fakeTelegram answers sendMessage calls like the Bot API and records them.
type fakeTelegram struct {
	mu   sync.Mutex
	sent []sentMessage
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var params struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&params)
	var chatID int64
	_, _ = fmt.Sscan(params.ChatID, &chatID)

	f.mu.Lock()
	f.sent = append(f.sent, sentMessage{ChatID: chatID, Text: params.Text})
	f.mu.Unlock()

	_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":%d,"type":"private"}}}`, chatID)
}

func (f *fakeTelegram) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage(nil), f.sent...)
}

func (f *fakeTelegram) messagesTo(chatID int64) []string {
	var texts []string
	for _, m := range f.messages() {
		if m.ChatID == chatID {
			texts = append(texts, m.Text)
		}
	}
	return texts
}

type stateCall struct {
	FeedID         int64
	LastEntry      *string
	ETag           *string
	LastModified   *string
	Hints          storage.PollHints
	NextPollAt     time.Time
	ErrorCount     int
	UnchangedCount int
}

type rescheduleCall struct {
	FeedID         int64
	NextPollAt     time.Time
	ErrorCount     int
	UnchangedCount int
}

// fakeStore records the scheduling writes of the poller. Methods the poller
// does not use are left to the embedded nil interface and panic if called.
type fakeStore struct {
	storage.AbonnementStorage

	mu          sync.Mutex
	states      []stateCall
	reschedules []rescheduleCall
	disabled    map[int64]string
	moved       map[int64]string
	mergeOnMove bool

	created       []createCall
	deleted       [][2]int64
	subscriptions map[int64][]storage.Feed
	disabledURLs  map[string]bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		disabled:      map[int64]string{},
		moved:         map[int64]string{},
		subscriptions: map[int64][]storage.Feed{},
		disabledURLs:  map[string]bool{},
	}
}

func (s *fakeStore) SetFeedState(feedID int64, lastEntry, etag, lastModified *string, hints storage.PollHints, nextPollAt time.Time, errorCount, unchangedCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, stateCall{feedID, lastEntry, etag, lastModified, hints, nextPollAt, errorCount, unchangedCount})
	return nil
}

func (s *fakeStore) Reschedule(feedID int64, nextPollAt time.Time, errorCount, unchangedCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reschedules = append(s.reschedules, rescheduleCall{feedID, nextPollAt, errorCount, unchangedCount})
	return nil
}

func (s *fakeStore) DisableFeed(feedID int64, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disabled[feedID] = reason
	return nil
}

func (s *fakeStore) MoveFeedURL(feedID int64, newURL string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moved[feedID] = newURL
	return s.mergeOnMove, nil
}

type pollEnv struct {
	h     *Handler
	store *fakeStore
	tg    *fakeTelegram
}

func newPollEnv(t *testing.T) *pollEnv {
	t.Helper()

	tg := &fakeTelegram{}
	tgServer := httptest.NewServer(tg)
	t.Cleanup(tgServer.Close)

	bot, err := telebot.NewBot(telebot.Settings{Token: "test", URL: tgServer.URL, Offline: true})
	if err != nil {
		t.Fatal(err)
	}

	tmpl, err := config.GetTemplate("does-not-exist.gohtml")
	if err != nil {
		t.Fatal(err)
	}

	store := newFakeStore()
	return &pollEnv{
		h: &Handler{
			Bot:     bot,
			Config:  &config.Config{Template: tmpl, Poll: testPollConfig()},
			DB:      &storage.DB{Abonnements: store},
			Fetcher: fetcher.New(),
			AdminID: adminID,
		},
		store: store,
		tg:    tg,
	}
}

func feedServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func abonnement(url string, chats ...int64) storage.Abonnement {
	ab := storage.Abonnement{Feed: storage.Feed{ID: 1, Url: url}}
	for _, id := range chats {
		ab.Chats = append(ab.Chats, storage.Chat{ID: id})
	}
	return ab
}

func rss(ttl string, guids ...string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>Test &amp; Feed</title><link>https://example.org</link>`)
	sb.WriteString(ttl)
	for _, guid := range guids {
		fmt.Fprintf(&sb, `<item><title>Item %s</title><link>https://www.example.org/%s</link><guid>%s</guid><description>Text %s</description></item>`, guid, guid, guid, guid)
	}
	sb.WriteString(`</channel></rss>`)
	return sb.String()
}

func TestPollSendsNewItemsOldestFirst(t *testing.T) {
	env := newPollEnv(t)
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", `"v2"`)
		_, _ = w.Write([]byte(rss("<ttl>120</ttl>", "3", "2", "1")))
	})

	ab := abonnement(srv.URL, 10, 11)
	ab.LastEntry = sql.NullString{String: "1", Valid: true}
	env.h.pollFeed(ab, nil)

	for _, chat := range []int64{10, 11} {
		msgs := env.tg.messagesTo(chat)
		if len(msgs) != 2 {
			t.Fatalf("chat %d got %d messages, want 2", chat, len(msgs))
		}
		if !strings.Contains(msgs[0], "Item 2") || !strings.Contains(msgs[1], "Item 3") {
			t.Errorf("chat %d messages not oldest first: %q", chat, msgs)
		}
		if !strings.Contains(msgs[0], "Test &amp; Feed") || !strings.Contains(msgs[0], "Weiterlesen auf example.org") {
			t.Errorf("unexpected message rendering: %q", msgs[0])
		}
	}
	if msgs := env.tg.messagesTo(adminID); len(msgs) != 0 {
		t.Errorf("admin got unexpected messages: %q", msgs)
	}

	if len(env.store.states) != 1 {
		t.Fatalf("SetFeedState called %d times, want 1", len(env.store.states))
	}
	state := env.store.states[0]
	if state.LastEntry == nil || *state.LastEntry != "3" {
		t.Errorf("last entry = %v, want 3", state.LastEntry)
	}
	if state.ETag == nil || *state.ETag != `"v2"` {
		t.Errorf("etag = %v, want \"v2\"", state.ETag)
	}
	if state.UnchangedCount != 0 || state.ErrorCount != 0 {
		t.Errorf("counters = %d/%d, want 0/0", state.ErrorCount, state.UnchangedCount)
	}
	if state.Hints.Interval != 2*time.Hour {
		t.Errorf("stored ttl hint = %s, want 2h", state.Hints.Interval)
	}
	assertDelay(t, state.NextPollAt, 2*time.Hour)
}

func TestPollUnchangedFeedIncreasesInterval(t *testing.T) {
	env := newPollEnv(t)
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rss("", "1")))
	})

	ab := abonnement(srv.URL, 10)
	ab.LastEntry = sql.NullString{String: "1", Valid: true}
	ab.UnchangedCount = 2
	env.h.pollFeed(ab, nil)

	if msgs := env.tg.messages(); len(msgs) != 0 {
		t.Errorf("unexpected messages: %v", msgs)
	}
	state := env.store.states[0]
	if state.UnchangedCount != 3 {
		t.Errorf("unchanged = %d, want 3", state.UnchangedCount)
	}
	assertDelay(t, state.NextPollAt, 40*time.Minute)
}

func TestPollNotModifiedUsesStoredHints(t *testing.T) {
	env := newPollEnv(t)
	var gotINM string
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotINM = r.Header.Get("If-None-Match")
		w.WriteHeader(http.StatusNotModified)
	})

	ab := abonnement(srv.URL, 10)
	ab.ETag = sql.NullString{String: `"v1"`, Valid: true}
	ab.FeedInterval = int((3 * time.Hour).Seconds())
	env.h.pollFeed(ab, nil)

	if gotINM != `"v1"` {
		t.Errorf("If-None-Match = %q, want stored etag", gotINM)
	}
	if len(env.store.states) != 0 {
		t.Errorf("304 must not overwrite the cached state")
	}
	if len(env.store.reschedules) != 1 {
		t.Fatalf("Reschedule called %d times, want 1", len(env.store.reschedules))
	}
	r := env.store.reschedules[0]
	if r.ErrorCount != 0 || r.UnchangedCount != 1 {
		t.Errorf("counters = %d/%d, want 0/1", r.ErrorCount, r.UnchangedCount)
	}
	assertDelay(t, r.NextPollAt, 3*time.Hour)
}

func TestPollGoneDisablesAndNotifiesAdmin(t *testing.T) {
	env := newPollEnv(t)
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte("<b>gone</b>"))
	})

	env.h.pollFeed(abonnement(srv.URL+"/?a=1&b=2", 10), nil)

	if reason := env.store.disabled[1]; reason != "HTTP 410 Gone" {
		t.Errorf("disabled reason = %q", reason)
	}
	if msgs := env.tg.messagesTo(10); len(msgs) != 0 {
		t.Errorf("subscriber must not get operational messages: %q", msgs)
	}
	admin := env.tg.messagesTo(adminID)
	if len(admin) != 1 {
		t.Fatalf("admin got %d messages, want 1", len(admin))
	}
	if !strings.Contains(admin[0], "a=1&amp;b=2") || !strings.Contains(admin[0], "&lt;b&gt;gone&lt;/b&gt;") {
		t.Errorf("admin message not escaped: %q", admin[0])
	}
}

func TestPollServerErrorBacksOff(t *testing.T) {
	env := newPollEnv(t)
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	ab := abonnement(srv.URL, 10)
	ab.ErrorCount = 2
	env.h.pollFeed(ab, nil)

	if len(env.store.disabled) != 0 {
		t.Errorf("feed must not be disabled yet")
	}
	r := env.store.reschedules[0]
	if r.ErrorCount != 3 {
		t.Errorf("error count = %d, want 3", r.ErrorCount)
	}
	assertDelay(t, r.NextPollAt, 40*time.Minute)
}

func TestPollRetiresFeedAfterLongFailure(t *testing.T) {
	env := newPollEnv(t)
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	recent := abonnement(srv.URL, 10)
	recent.ErrorCount = 50
	recent.FailingSince = sql.NullTime{Time: time.Now().Add(-24 * time.Hour), Valid: true}
	env.h.pollFeed(recent, nil)
	if len(env.store.disabled) != 0 {
		t.Fatalf("feed failing for one day must not be retired")
	}

	old := abonnement(srv.URL, 10)
	old.ErrorCount = maxFeedErrors - 1
	old.FailingSince = sql.NullTime{Time: time.Now().Add(-8 * 24 * time.Hour), Valid: true}
	env.h.pollFeed(old, nil)
	if reason := env.store.disabled[1]; reason != "HTTP 404" {
		t.Errorf("disabled reason = %q, want HTTP 404", reason)
	}
}

func TestPollRateLimitKeepsErrorCount(t *testing.T) {
	env := newPollEnv(t)
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	ab := abonnement(srv.URL, 10)
	ab.ErrorCount = 1
	env.h.pollFeed(ab, nil)

	r := env.store.reschedules[0]
	if r.ErrorCount != 1 {
		t.Errorf("error count = %d, want unchanged 1", r.ErrorCount)
	}
	assertDelay(t, r.NextPollAt, time.Hour)
	if len(env.tg.messagesTo(adminID)) != 1 {
		t.Errorf("admin should be notified about 429")
	}
}

func TestPollPermanentRedirect(t *testing.T) {
	target := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rss("", "1")))
	})
	src := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusMovedPermanently)
	})

	t.Run("renamed", func(t *testing.T) {
		env := newPollEnv(t)
		env.h.pollFeed(abonnement(src.URL, 10), nil)

		if env.store.moved[1] != target.URL {
			t.Errorf("moved to %q, want %q", env.store.moved[1], target.URL)
		}
		if len(env.store.states) != 1 {
			t.Errorf("state of the renamed feed must be saved")
		}
		if len(env.tg.messagesTo(adminID)) != 1 {
			t.Errorf("admin should be notified about the move")
		}
	})

	t.Run("merged", func(t *testing.T) {
		env := newPollEnv(t)
		env.store.mergeOnMove = true
		env.h.pollFeed(abonnement(src.URL, 10), nil)

		if len(env.store.states) != 0 || len(env.tg.messagesTo(10)) != 0 {
			t.Errorf("merged feed must be left to the surviving feed")
		}
	})
}

func TestPollTemplateErrorStillSavesState(t *testing.T) {
	env := newPollEnv(t)
	env.h.Config.Template = template.Must(template.New("post").Parse("{{.Missing}}"))
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rss("", "2", "1")))
	})

	env.h.pollFeed(abonnement(srv.URL, 10), nil)

	if len(env.tg.messages()) != 0 {
		t.Errorf("no message can be rendered")
	}
	if len(env.store.states) != 1 {
		t.Fatalf("state must be saved even if rendering fails")
	}
}

func TestPollFeedsSerializesSameHost(t *testing.T) {
	env := newPollEnv(t)
	var inFlight, maxInFlight, requests atomic.Int32
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		requests.Add(1)
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusNotModified)
	})

	var abonnements []storage.Abonnement
	for i := 1; i <= 5; i++ {
		ab := abonnement(fmt.Sprintf("%s/feed%d", srv.URL, i), 10)
		ab.ID = int64(i)
		abonnements = append(abonnements, ab)
	}
	env.h.pollFeeds(abonnements, nil)

	if requests.Load() != 5 {
		t.Errorf("got %d requests, want 5", requests.Load())
	}
	if maxInFlight.Load() != 1 {
		t.Errorf("max concurrent requests to one host = %d, want 1", maxInFlight.Load())
	}
}
