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

var (
	feedproxyRe = regexp.MustCompile("^https?://feedproxy.google.com/~r/(.+?)/.*")
	blankLineRe = regexp.MustCompile("(?m)^\\s*$[\r\n]*")
)

// compiledReplacement is a content filter with its regex compiled once per poll
// cycle instead of once per feed item.
type compiledReplacement struct {
	re      *regexp.Regexp // nil for a literal replacement
	literal string
}

func compileReplacements(replacements []storage.Replacement) []compiledReplacement {
	compiled := make([]compiledReplacement, 0, len(replacements))
	for _, r := range replacements {
		if r.IsRegex {
			re, err := regexp.Compile(r.Value)
			if err != nil {
				log.Printf("skipping invalid replacement regex %q: %s", r.Value, err)
				continue
			}
			compiled = append(compiled, compiledReplacement{re: re})
		} else {
			compiled = append(compiled, compiledReplacement{literal: r.Value})
		}
	}
	return compiled
}

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
	h.pollFeeds(abonnements, compileReplacements(replacements))
}

// pollFeeds groups the due feeds by host and hands each group to a worker, so
// requests to the same host are serialized (FRB033/034) without idle workers
// blocking on a busy host.
func (h *Handler) pollFeeds(abonnements []storage.Abonnement, replacements []compiledReplacement) {
	workers := h.Config.Poll.Concurrency
	if workers < 1 {
		workers = 1
	}

	groups := make(map[string][]storage.Abonnement)
	var hosts []string
	for _, abonnement := range abonnements {
		host := feedHost(abonnement.Feed.Url)
		if _, ok := groups[host]; !ok {
			hosts = append(hosts, host)
		}
		groups[host] = append(groups[host], abonnement)
	}

	jobs := make(chan []storage.Abonnement)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range jobs {
				for _, abonnement := range group {
					h.pollFeed(abonnement, replacements)
				}
			}
		}()
	}

	for _, host := range hosts {
		jobs <- groups[host]
	}
	close(jobs)

	wg.Wait()
}

func (h *Handler) pollFeed(abonnement storage.Abonnement, replacements []compiledReplacement) {
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
		merged, err := h.DB.Abonnements.MoveFeedURL(feed.ID, result.PermanentURL)
		if err != nil {
			log.Printf("%s: could not update url to %s: %s", feed.Url, result.PermanentURL, err)
		} else {
			h.notify(abonnement, fmt.Sprintf("ℹ️ Feed wurde dauerhaft umgezogen:\n%s\n→ %s",
				feed.Url, result.PermanentURL))
			if merged {
				// This feed row is gone; the existing feed at the target URL
				// now owns these subscriptions and will deliver the content.
				return
			}
			feed.Url = result.PermanentURL
		}
	}

	switch {
	case result.Status == 200 && result.Feed != nil:
		h.handleOK(abonnement, feed, result, replacements)
	case result.NotModified:
		hints := feed.Hints()
		result.FeedInterval, result.SkipHours, result.SkipDays = hints.Interval, hints.SkipHours, hints.SkipDays
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

func (h *Handler) handleOK(abonnement storage.Abonnement, feed storage.Feed, result *fetcher.Result, replacements []compiledReplacement) {
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
			templateData.Content = processContent(entry.Content, replacements)
		} else if entry.Description != "" {
			templateData.Content = processContent(entry.Description, replacements)
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
		storage.PollHints{Interval: result.FeedInterval, SkipHours: result.SkipHours, SkipDays: result.SkipDays},
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
	}
	delay = min(delay, h.Config.Poll.IntervalMax)

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
// (max-age/ttl, FRB022/024, capped at POLL_INTERVAL_MAX), shifted out of
// skipHours/skipDays (FRB024).
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
		hint := min(max(result.MaxAge, result.FeedInterval), h.Config.Poll.IntervalMax)
		interval = max(interval, hint)
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

// notify sends operational messages (disable, redirect, rate-limit) to the
// admin only — never into the subscriber chats/channels, which are reserved for
// feed content.
func (h *Handler) notify(abonnement storage.Abonnement, text string) {
	if h.AdminID == 0 {
		return
	}
	if err := h.sendText(h.AdminID, text, abonnement.Feed.Url); err != nil {
		log.Printf("%s: notify failed: %s", abonnement.Feed.Url, err)
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

func processContent(content string, replacements []compiledReplacement) string {
	processed := html.UnescapeString(content)

	for _, replacement := range replacements {
		if replacement.re != nil {
			processed = replacement.re.ReplaceAllString(processed, "")
		} else {
			processed = strings.ReplaceAll(processed, replacement.literal, "")
		}
	}

	processed = blankLineRe.ReplaceAllString(processed, "")
	processed = strings.TrimSpace(processed)

	if len(processed) > 270 {
		return processed[:270] + "..."
	}

	return processed
}

func (h *Handler) sendText(chatId int64, text string, url string) error {
	_, err := h.Bot.Send(telebot.ChatID(chatId), text, defaultSendOptions)
	if err == nil {
		return nil
	}

	// telebot returns FloodError by value, so match the value type.
	var floodError telebot.FloodError
	if errors.As(err, &floodError) {
		log.Printf("%s: Flood error, retrying after: %d seconds", url, floodError.RetryAfter)
		time.Sleep(time.Duration(floodError.RetryAfter) * time.Second)
		return h.sendText(chatId, text, url)
	}
	return err
}
