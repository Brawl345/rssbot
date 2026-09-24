package handler

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/Brawl345/rssbot/storage"
	"gopkg.in/telebot.v3"
)

func (h *Handler) OnSubscribe(c telebot.Context) error {
	args := c.Args()

	if len(args) == 0 || len(args) > 2 {
		return nil
	}

	feedUrl, err := sanitizeFeedURL(args[0])
	if err != nil {
		// FRB060/061: report the problem, don't guess at a corrected URL.
		return c.Send("❌ Ungültige URL: "+err.Error(), defaultSendOptions)
	}

	chatId := c.Chat().ID
	var chatTitle string
	if c.Message().Private() {
		chatTitle = c.Chat().FirstName
	} else {
		chatTitle = c.Chat().Title
	}

	if len(args) == 2 {
		// Chat ID given
		chatInfo, err := h.Bot.ChatByUsername(args[1])
		if err != nil {
			return c.Send("❌ Diese Gruppe oder dieser Kanal existiert nicht.", defaultSendOptions)
		}

		userInfo, err := h.Bot.ChatMemberOf(chatInfo, h.Bot.Me)
		if err != nil {
			return c.Send("❌ Diese Gruppe oder dieser Kanal existiert nicht.", defaultSendOptions)
		}

		if chatInfo.Type == telebot.ChatChannel && !userInfo.CanPostMessages {
			return c.Send("❌ Du musst dem Bot die Berechtigung zum Posten erteilen.", defaultSendOptions)
		}

		chatId = chatInfo.ID
		chatTitle = chatInfo.Title
	}

	// FRB036: a single request gathers everything needed to add the feed.
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	result, err := h.Fetcher.Fetch(ctx, feedUrl, "", "")
	if err != nil {
		log.Printf("subscribe %s: %s", feedUrl, err)
		if result != nil && result.Body != "" {
			// FRB101: surface the server's response so the user can act on it.
			return c.Send(fmt.Sprintf("❌ Ungültiger Feed (HTTP %d):\n<pre>%s</pre>",
				result.Status, html.EscapeString(result.Body)), defaultSendOptions)
		}
		return c.Send("❌ Feed konnte nicht abgerufen werden: "+html.EscapeString(err.Error()), defaultSendOptions)
	}

	// FRB100: only add feeds that actually answer with 200.
	if result.Status != 200 {
		msg := fmt.Sprintf("❌ Der Server antwortete mit HTTP %d.", result.Status)
		if result.Body != "" {
			msg += fmt.Sprintf("\n<pre>%s</pre>", html.EscapeString(result.Body))
		}
		return c.Send(msg, defaultSendOptions)
	}

	// FRB102: must be a real feed.
	feed := result.Feed
	if feed == nil {
		return c.Send("❌ Diese URL liefert keinen gültigen Feed.", defaultSendOptions)
	}

	if feed.FeedLink != "" {
		if normalized, err := sanitizeFeedURL(feed.FeedLink); err == nil {
			feedUrl = normalized
		}
	}

	nextPollAt := h.nextPoll(0, result)

	// The feed answered with a valid 200, so a previously retired feed works again.
	reactivated, err := h.DB.Abonnements.ReactivateFeed(feedUrl, nextPollAt)
	if err != nil {
		log.Printf("subscribe %s: could not reactivate: %s", feedUrl, err)
	}

	exists, _ := h.DB.Abonnements.ExistsByFeedUrl(chatId, feedUrl)
	if exists {
		if reactivated {
			return c.Send("✅ Der deaktivierte Feed wurde wieder aktiviert.", defaultSendOptions)
		}
		return c.Send("✅ Du hast diesen Feed bereits abonniert.", defaultSendOptions)
	}

	var lastEntry *string
	if len(feed.Items) > 0 {
		if feed.Items[0].GUID != "" {
			lastEntry = &feed.Items[0].GUID
		} else {
			lastEntry = &feed.Items[0].Link
		}
	}

	// Persist the cache validators from this fetch so the very first scheduled
	// poll is already conditional.
	etag := nullableString(result.ETag)
	lastModified := nullableString(result.LastModified)
	hints := storage.PollHints{Interval: result.FeedInterval, SkipHours: result.SkipHours, SkipDays: result.SkipDays}

	err = h.DB.Abonnements.Create(chatId, chatTitle, feedUrl, lastEntry, etag, lastModified, hints, nextPollAt)
	if err != nil {
		log.Println(err)
		return c.Send("❌ Beim Abonnieren des Feeds ist ein Fehler aufgetreten.", defaultSendOptions)
	}

	return c.Send("✅ Der Feed wurde erfolgreich abonniert!", defaultSendOptions)
}

// sanitizeFeedURL trims surrounding whitespace and rejects URLs containing
// characters that should never appear in a real feed URL (FRB060/061). It does
// not rewrite or guess — it only validates.
func sanitizeFeedURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("leer")
	}

	for _, r := range trimmed {
		if r <= ' ' || r == '"' || r == '\'' || r == '<' || r == '>' || r == '`' {
			return "", fmt.Errorf("enthält ungültige Zeichen")
		}
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("nicht parsebar")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("muss mit http:// oder https:// beginnen")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("kein Host")
	}

	return trimmed, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
