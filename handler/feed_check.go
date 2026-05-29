package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Brawl345/rssbot/fetcher"
	"github.com/Brawl345/rssbot/storage"
	"github.com/mmcdole/gofeed"
	"gopkg.in/telebot.v3"
)

const (
	// maxFeedErrors is how many consecutive failures a feed tolerates before it
	// is retired (FRB110-119).
	maxFeedErrors = 12
	// fetchTimeout bounds a single feed fetch.
	fetchTimeout = 35 * time.Second
)

var feedproxyRe = regexp.MustCompile("^https?://feedproxy.google.com/~r/(.+?)/.*")

type TemplateData struct {
	Title      string
	FeedTitle  string
	Content    string
	PostLink   string
	PostDomain string
}

// OnCheck is the scheduler tick. It polls only feeds that are currently due and
// reschedules itself after POLL_TICK. Because due-ness lives in the database,
// a process restart never causes a mass re-fetch (FRB037).
func (h *Handler) OnCheck() {
	defer time.AfterFunc(h.Config.Poll.Tick, h.OnCheck)

	abonnements, err := h.DB.Abonnements.GetDue()
	if err != nil {
		log.Println(err)
		return
	}
	if len(abonnements) == 0 {
		return
	}

	replacements, err := h.DB.Replacements.List()
	if err != nil {
		log.Println(err)
		return
	}

	log.Printf("Polling %d due feed(s)", len(abonnements))
	h.pollFeeds(abonnements, replacements)
}

// pollFeeds fetches the due feeds through a bounded worker pool while
// serializing requests that target the same host (FRB033/034).
func (h *Handler) pollFeeds(abonnements []storage.Abonnement, replacements []storage.Replacement) {
	sem := make(chan struct{}, h.Config.Poll.Concurrency)
	hosts := &hostLocks{m: make(map[string]*sync.Mutex)}
	var wg sync.WaitGroup

	for _, abonnement := range abonnements {
		abonnement := abonnement
		wg.Add(1)
		go func() {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			lock := hosts.get(feedHost(abonnement.Feed.Url))
			lock.Lock()
			defer lock.Unlock()

			h.pollFeed(abonnement, replacements)
		}()
	}

	wg.Wait()
}

func (h *Handler) pollFeed(abonnement storage.Abonnement, replacements []storage.Replacement) {
	feed := abonnement.Feed

	var etag, lastModified string
	if feed.ETag.Valid {
		etag = feed.ETag.String
	}
	if feed.LastModified.Valid {
		lastModified = feed.LastModified.String
	}

	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	result, err := h.Fetcher.Fetch(ctx, feed.Url, etag, lastModified)
	if err != nil {
		h.handleSoftError(abonnement, feed, err.Error(), bodyOf(result))
		return
	}

	// Permanent move: persist the new URL and stop polling the old one
	// (FRB130/131/132).
	if result.PermanentURL != "" {
		if err := h.DB.Abonnements.SetFeedURL(feed.ID, result.PermanentURL); err != nil {
			log.Printf("%s: could not update url to %s: %s", feed.Url, result.PermanentURL, err)
		} else {
			h.notify(abonnement, fmt.Sprintf("ℹ️ Feed wurde dauerhaft umgezogen:\n%s\n→ %s",
				feed.Url, result.PermanentURL))
			feed.Url = result.PermanentURL
		}
	}

	switch {
	case result.Status == 200 && result.Feed != nil:
		h.handleOK(abonnement, feed, result, replacements)
	case result.NotModified:
		next := h.nextPoll(feed.UnchangedCount+1, result)
		if err := h.DB.Abonnements.Reschedule(feed.ID, next, 0, feed.UnchangedCount+1); err != nil {
			log.Printf("%s: reschedule failed: %s", feed.Url, err)
		}
	case result.Status == 429 || result.Status == 503:
		h.handleRateLimit(abonnement, feed, result)
	case result.Status == 410:
		h.disable(abonnement, feed, "HTTP 410 Gone", result.Body)
	default:
		h.handleSoftError(abonnement, feed, fmt.Sprintf("HTTP %d", result.Status), result.Body)
	}
}

func (h *Handler) handleOK(abonnement storage.Abonnement, feed storage.Feed, result *fetcher.Result, replacements []storage.Replacement) {
	gfeed := result.Feed

	var lastEntry *string
	if feed.LastEntry.Valid {
		lastEntry = &feed.LastEntry.String
	}
	newItems := filterNewItems(gfeed.Items, lastEntry)

	for _, entry := range reverse(newItems) {
		templateData := &TemplateData{}
		if entry.Title != "" {
			templateData.Title = html.EscapeString(entry.Title)
		} else {
			templateData.Title = "Kein Titel"
		}

		templateData.FeedTitle = html.EscapeString(gfeed.Title)

		if entry.Content != "" {
			templateData.Content = processContent(entry.Content, &replacements)
		} else if entry.Description != "" {
			templateData.Content = processContent(entry.Description, &replacements)
		}

		if entry.Link != "" {
			templateData.PostLink = entry.Link
		} else {
			templateData.PostLink = gfeed.Link
		}

		match := feedproxyRe.FindStringSubmatch(templateData.PostLink)
		if len(match) > 0 {
			templateData.PostDomain = match[1]
		} else {
			parsedUrl, _ := url.Parse(templateData.PostLink)
			templateData.PostDomain = parsedUrl.Host
		}
		templateData.PostDomain = strings.Replace(templateData.PostDomain, "www.", "", 1)

		var tpl bytes.Buffer
		if err := h.Config.Template.Execute(&tpl, templateData); err != nil {
			log.Printf("%s: %s", feed.Url, err)
			return
		}

		for _, chat := range abonnement.Chats {
			if err := h.sendText(chat.ID, tpl.String(), feed.Url); err != nil {
				log.Printf("%s: %s", feed.Url, err)
			}
		}
	}

	newLastEntry := lastEntry
	if len(gfeed.Items) > 0 {
		if gfeed.Items[0].GUID != "" {
			newLastEntry = &gfeed.Items[0].GUID
		} else {
			newLastEntry = &gfeed.Items[0].Link
		}
	}

	// FRB023: only reset the adaptive counter when genuinely new content arrived.
	unchanged := feed.UnchangedCount + 1
	if len(newItems) > 0 {
		unchanged = 0
	}

	next := h.nextPoll(unchanged, result)
	err := h.DB.Abonnements.SetFeedState(feed.ID, newLastEntry,
		nullableString(result.ETag), nullableString(result.LastModified),
		next, 0, unchanged)
	if err != nil {
		log.Printf("%s: could not save state: %s", feed.Url, err)
	}
}

func (h *Handler) handleRateLimit(abonnement storage.Abonnement, feed storage.Feed, result *fetcher.Result) {
	delay := result.RetryAfter
	if delay <= 0 {
		// FRB021: a 429/503 without a hint is still "slow down".
		delay = h.Config.Poll.Interval * 4
		if delay > h.Config.Poll.IntervalMax {
			delay = h.Config.Poll.IntervalMax
		}
	}

	// Keep the cached validators (FRB016) and do not count this toward retirement.
	if err := h.DB.Abonnements.Reschedule(feed.ID, time.Now().Add(delay), feed.ErrorCount, feed.UnchangedCount); err != nil {
		log.Printf("%s: reschedule failed: %s", feed.Url, err)
	}
	log.Printf("%s: HTTP %d, backing off %s", feed.Url, result.Status, delay.Round(time.Second))

	if result.Status == 429 {
		h.notify(abonnement, fmt.Sprintf(
			"⚠️ Feed sendet \"429 Too Many Requests\":\n%s\nBackoff: %s. Eventuell ist das Poll-Intervall zu kurz.",
			feed.Url, delay.Round(time.Second)))
	}
}

// handleSoftError applies exponential backoff and retires the feed once it has
// failed too many times in a row (FRB110-119).
func (h *Handler) handleSoftError(abonnement storage.Abonnement, feed storage.Feed, reason, body string) {
	errorCount := feed.ErrorCount + 1
	if errorCount >= maxFeedErrors {
		h.disable(abonnement, feed, reason, body)
		return
	}

	delay := h.errorBackoff(errorCount)
	if err := h.DB.Abonnements.Reschedule(feed.ID, time.Now().Add(delay), errorCount, feed.UnchangedCount); err != nil {
		log.Printf("%s: reschedule failed: %s", feed.Url, err)
	}
	log.Printf("%s: %s (error %d/%d), retrying in %s", feed.Url, reason, errorCount, maxFeedErrors, delay.Round(time.Second))
}

func (h *Handler) disable(abonnement storage.Abonnement, feed storage.Feed, reason, body string) {
	if err := h.DB.Abonnements.DisableFeed(feed.ID, reason); err != nil {
		log.Printf("%s: could not disable: %s", feed.Url, err)
		return
	}
	log.Printf("%s: disabled (%s)", feed.Url, reason)

	msg := fmt.Sprintf("🚫 Feed wurde deaktiviert:\n%s\nGrund: %s", feed.Url, reason)
	if body != "" {
		msg += fmt.Sprintf("\n<pre>%s</pre>", html.EscapeString(body))
	}
	h.notify(abonnement, msg)
}

// nextPoll computes the next poll time: base interval, optionally stretched for
// feeds that rarely change (FRB023), never faster than server hints
// (max-age/ttl, FRB022/024), shifted out of skipHours/skipDays (FRB024).
func (h *Handler) nextPoll(unchangedCount int, result *fetcher.Result) time.Time {
	interval := h.Config.Poll.Interval

	if h.Config.Poll.Adaptive && unchangedCount > 0 {
		for i := 0; i < unchangedCount && interval < h.Config.Poll.IntervalMax; i++ {
			interval *= 2
		}
		if interval > h.Config.Poll.IntervalMax {
			interval = h.Config.Poll.IntervalMax
		}
	}

	if result != nil {
		if result.MaxAge > interval {
			interval = result.MaxAge
		}
		if result.FeedInterval > interval {
			interval = result.FeedInterval
		}
	}

	next := time.Now().Add(interval)
	if result != nil {
		next = applySkip(next, result.SkipHours, result.SkipDays)
	}
	return next
}

func (h *Handler) errorBackoff(errorCount int) time.Duration {
	interval := h.Config.Poll.Interval
	for i := 1; i < errorCount && interval < h.Config.Poll.IntervalMax; i++ {
		interval *= 2
	}
	if interval > h.Config.Poll.IntervalMax {
		interval = h.Config.Poll.IntervalMax
	}
	return interval
}

func (h *Handler) notify(abonnement storage.Abonnement, text string) {
	for _, chat := range abonnement.Chats {
		if err := h.sendText(chat.ID, text, abonnement.Feed.Url); err != nil {
			log.Printf("%s: notify failed: %s", abonnement.Feed.Url, err)
		}
	}
}

// applySkip pushes t forward in whole hours until it is outside the feed's
// declared skipHours/skipDays (RSS uses UTC). Capped so an over-eager feed can
// never block polling forever.
func applySkip(t time.Time, skipHours []int, skipDays []string) time.Time {
	if len(skipHours) == 0 && len(skipDays) == 0 {
		return t
	}
	for i := 0; i < 48; i++ {
		u := t.UTC()
		if containsInt(skipHours, u.Hour()) || containsDay(skipDays, u.Weekday().String()) {
			t = t.Add(time.Hour)
			continue
		}
		break
	}
	return t
}

func filterNewItems(items []*gofeed.Item, lastEntry *string) []*gofeed.Item {
	if lastEntry == nil {
		return items
	}
	for i, item := range items {
		if item.GUID == *lastEntry || item.Link == *lastEntry {
			return items[:i]
		}
	}
	return items
}

func feedHost(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return strings.ToLower(u.Host)
	}
	return rawURL
}

func bodyOf(result *fetcher.Result) string {
	if result == nil {
		return ""
	}
	return result.Body
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func containsDay(days []string, day string) bool {
	for _, d := range days {
		if strings.EqualFold(strings.TrimSpace(d), day) {
			return true
		}
	}
	return false
}

type hostLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (h *hostLocks) get(host string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	if lock, ok := h.m[host]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	h.m[host] = lock
	return lock
}

func processContent(content string, replacements *[]storage.Replacement) string {
	processed := html.UnescapeString(content)

	for _, replacement := range *replacements {
		if replacement.IsRegex {
			re := regexp.MustCompile(replacement.Value)
			processed = re.ReplaceAllString(processed, "")
		} else {
			processed = strings.ReplaceAll(processed, replacement.Value, "")
		}
	}

	processed = regexp.MustCompile("(?m)^\\s*$[\r\n]*").ReplaceAllString(processed, "")
	processed = strings.TrimSpace(processed)

	if len(processed) > 270 {
		return processed[:270] + "..."
	}

	return processed
}

func (h *Handler) sendText(chatId int64, text string, url string) error {
	_, err := h.Bot.Send(telebot.ChatID(chatId), text, defaultSendOptions)

	var floodError *telebot.FloodError

	if err != nil {
		if errors.As(err, &floodError) {
			log.Printf("%s: Flood error, retrying after: %d seconds", url,
				floodError.RetryAfter)
			time.Sleep(time.Duration(err.(telebot.FloodError).RetryAfter) * time.Second)
			err := h.sendText(chatId, text, url)
			if err != nil {
				return err
			}
		} else {
			return err
		}

	}
	return nil
}
