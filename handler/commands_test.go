package handler

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Brawl345/rssbot/storage"
	"gopkg.in/telebot.v3"
)

const userID = 10

type createCall struct {
	ChatID       int64
	FeedURL      string
	LastEntry    *string
	ETag         *string
	LastModified *string
	Hints        storage.PollHints
	NextPollAt   time.Time
}

func (s *fakeStore) Create(chatID int64, chatTitle string, feedURL string, lastEntry, etag, lastModified *string, hints storage.PollHints, nextPollAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, createCall{chatID, feedURL, lastEntry, etag, lastModified, hints, nextPollAt})
	s.subscriptions[chatID] = append(s.subscriptions[chatID], storage.Feed{Url: feedURL})
	return nil
}

func (s *fakeStore) Delete(chatID int64, feedID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, [2]int64{chatID, feedID})
	return nil
}

func (s *fakeStore) ExistsByFeedUrl(chatID int64, feedURL string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.subscriptions[chatID] {
		if f.Url == feedURL {
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) ExistsById(chatID int64, feedID int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.subscriptions[chatID] {
		if f.ID == feedID {
			return true, nil
		}
	}
	return false, nil
}

func (s *fakeStore) GetByUser(chatID int64) ([]storage.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subscriptions[chatID], nil
}

func (s *fakeStore) ReactivateFeed(feedURL string, nextPollAt time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	was := s.disabledURLs[feedURL]
	delete(s.disabledURLs, feedURL)
	return was, nil
}

func (env *pollEnv) command(t *testing.T, handler telebot.HandlerFunc, payload string) string {
	t.Helper()
	before := len(env.tg.messagesTo(userID))
	ctx := env.h.Bot.NewContext(telebot.Update{Message: &telebot.Message{
		Payload: payload,
		Chat:    &telebot.Chat{ID: userID, Type: telebot.ChatPrivate, FirstName: "Tester"},
	}})
	if err := handler(ctx); err != nil {
		t.Fatalf("handler returned %v", err)
	}
	msgs := env.tg.messagesTo(userID)
	if len(msgs) != before+1 {
		t.Fatalf("got %d replies, want 1", len(msgs)-before)
	}
	return msgs[len(msgs)-1]
}

func TestSubscribeRejectsInvalidInput(t *testing.T) {
	html := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>no feed</body></html>"))
	})
	missing := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"bad scheme", "ftp://example.org/feed", "Ungültige URL"},
		{"bad characters", "https://example.org/<feed>", "Ungültige URL"},
		{"not found", missing.URL, "HTTP 404"},
		{"not a feed", html.URL, "Ungültiger Feed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newPollEnv(t)
			reply := env.command(t, env.h.OnSubscribe, tt.payload)
			if !strings.Contains(reply, tt.want) {
				t.Errorf("reply = %q, want %q", reply, tt.want)
			}
			if len(env.store.created) != 0 {
				t.Errorf("feed must not be created")
			}
		})
	}
}

func TestSubscribeStoresValidatorsAndHints(t *testing.T) {
	env := newPollEnv(t)
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", `"abc"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		_, _ = w.Write([]byte(rss("<ttl>120</ttl>", "2", "1")))
	})

	reply := env.command(t, env.h.OnSubscribe, srv.URL)
	if !strings.Contains(reply, "erfolgreich abonniert") {
		t.Fatalf("reply = %q", reply)
	}
	if len(env.store.created) != 1 {
		t.Fatalf("Create called %d times, want 1", len(env.store.created))
	}
	c := env.store.created[0]
	if c.ChatID != userID || c.FeedURL != srv.URL {
		t.Errorf("created %d/%q", c.ChatID, c.FeedURL)
	}
	if c.LastEntry == nil || *c.LastEntry != "2" {
		t.Errorf("last entry = %v, want newest item", c.LastEntry)
	}
	if c.ETag == nil || *c.ETag != `"abc"` || c.LastModified == nil {
		t.Errorf("validators not stored: etag=%v lm=%v", c.ETag, c.LastModified)
	}
	if c.Hints.Interval != 2*time.Hour {
		t.Errorf("ttl hint = %s, want 2h", c.Hints.Interval)
	}
	assertDelay(t, c.NextPollAt, 2*time.Hour)
}

func TestSubscribeUsesSelfLink(t *testing.T) {
	env := newPollEnv(t)
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
<title>T</title><link>https://example.org</link>
<atom:link href="https://example.org/canonical.xml" rel="self" type="application/rss+xml"/>
<item><title>A</title><guid>a</guid></item></channel></rss>`))
	})

	env.command(t, env.h.OnSubscribe, srv.URL)
	if got := env.store.created[0].FeedURL; got != "https://example.org/canonical.xml" {
		t.Errorf("subscribed to %q, want the self link", got)
	}
}

func TestSubscribeExisting(t *testing.T) {
	srv := feedServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rss("", "1")))
	})

	t.Run("already subscribed", func(t *testing.T) {
		env := newPollEnv(t)
		env.store.subscriptions[userID] = []storage.Feed{{Url: srv.URL}}
		if reply := env.command(t, env.h.OnSubscribe, srv.URL); !strings.Contains(reply, "bereits abonniert") {
			t.Errorf("reply = %q", reply)
		}
		if len(env.store.created) != 0 {
			t.Errorf("duplicate subscription created")
		}
	})

	t.Run("disabled feed is reactivated", func(t *testing.T) {
		env := newPollEnv(t)
		env.store.subscriptions[userID] = []storage.Feed{{Url: srv.URL}}
		env.store.disabledURLs[srv.URL] = true
		if reply := env.command(t, env.h.OnSubscribe, srv.URL); !strings.Contains(reply, "wieder aktiviert") {
			t.Errorf("reply = %q", reply)
		}
		if env.store.disabledURLs[srv.URL] {
			t.Errorf("feed still disabled")
		}
	})
}

func TestUnsubscribe(t *testing.T) {
	env := newPollEnv(t)
	env.store.subscriptions[userID] = []storage.Feed{{ID: 7, Url: "https://example.org/feed"}}

	if reply := env.command(t, env.h.OnUnsubscribe, "abc"); !strings.Contains(reply, "Feed-ID") {
		t.Errorf("reply = %q", reply)
	}
	if reply := env.command(t, env.h.OnUnsubscribe, "8"); !strings.Contains(reply, "nicht abonniert") {
		t.Errorf("reply = %q", reply)
	}
	if reply := env.command(t, env.h.OnUnsubscribe, "7"); !strings.Contains(reply, "deabonniert") {
		t.Errorf("reply = %q", reply)
	}
	if len(env.store.deleted) != 1 || env.store.deleted[0] != [2]int64{userID, 7} {
		t.Errorf("deleted = %v, want [[%d 7]]", env.store.deleted, userID)
	}
}

func TestListShowsDisabledFeeds(t *testing.T) {
	env := newPollEnv(t)
	env.store.subscriptions[userID] = []storage.Feed{
		{ID: 1, Url: "https://example.org/a?x=1&y=2"},
		{ID: 2, Url: "https://example.org/b", Disabled: true, DisabledReason: sql.NullString{String: "HTTP 410 Gone", Valid: true}},
	}

	reply := env.command(t, env.h.OnList, "")
	if !strings.Contains(reply, "a?x=1&amp;y=2") {
		t.Errorf("URL not escaped: %q", reply)
	}
	if strings.Count(reply, "🚫") != 1 || !strings.Contains(reply, "HTTP 410 Gone") {
		t.Errorf("disabled marker missing: %q", reply)
	}
}
