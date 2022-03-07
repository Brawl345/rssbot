package handler

import (
	"errors"
	"fmt"
	"github.com/Brawl345/rssbot/storage"
	"gopkg.in/telebot.v3"
	"html"
	"log"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

func (h Handler) OnCheck() {
	var wg sync.WaitGroup
	log.Println("===============================/")
	abonnements, err := h.DB.Abonnements.GetAll()
	if err != nil {
		log.Println(err)
		time.AfterFunc(1*time.Minute, h.OnCheck)
		return
	}

	replacements, err := h.DB.Replacements.List()
	if err != nil {
		log.Println(err)
		time.AfterFunc(1*time.Minute, h.OnCheck)
		return
	}

	if len(abonnements) == 0 {
		log.Println("No feeds found, checkin again in 60 seconds")
	}

	for _, abonnement := range abonnements {
		abonnement := abonnement
		wg.Add(1)
		go func() {
			defer wg.Done()
			sb := strings.Builder{}

			log.Printf("%s", abonnement.Feed.Url)

			var lastEntry *string
			if abonnement.LastEntry.Valid {
				lastEntry = &abonnement.LastEntry.String
			}

			feed, err := abonnement.Feed.Check(lastEntry)
			if err != nil {
				log.Printf("%s: %s", abonnement.Feed.Url, err)
				return
			}

			for _, entry := range reverse(feed.Items) {
				if entry.Title != "" {
					sb.WriteString(fmt.Sprintf("<b>%s</b>\n", html.EscapeString(entry.Title)))
				} else {
					sb.WriteString("<b>Kein Titel</b>\n")
				}

				sb.WriteString(fmt.Sprintf("<i>%s</i>\n", feed.Title))

				if entry.Content != "" {
					sb.WriteString(processContent(entry.Content, &replacements))
					sb.WriteString("\n")
				} else if entry.Description != "" {
					sb.WriteString(processContent(entry.Description, &replacements))
					sb.WriteString("\n")
				}

				var postLink string
				if entry.Link != "" {
					postLink = entry.Link
				} else {
					postLink = feed.Link
				}

				re := regexp.MustCompile("^https?://feedproxy.google.com/~r/(.+?)/.*")
				match := re.FindStringSubmatch(postLink)

				var linkName string
				if len(match) > 0 {
					linkName = match[1]
				} else {
					parsedUrl, _ := url.Parse(postLink)
					linkName = parsedUrl.Host
				}

				linkName = strings.Replace(linkName, "www.", "", 1)

				sb.WriteString(fmt.Sprintf("<a href=\"%s\">Weiterlesen auf %s</a>", postLink, linkName))

				for _, chat := range abonnement.Chats {
					err = h.sendText(chat.ID, sb.String(), abonnement.Feed.Url)
					if err != nil {
						log.Printf("%s: %s", abonnement.Feed.Url, err)
					}
				}

				sb.Reset()
			}
			if len(feed.Items) > 0 {
				var lastEntry *string
				if feed.Items[0].GUID != "" {
					lastEntry = &feed.Items[0].GUID
				} else {
					lastEntry = &feed.Items[0].Link
				}
				h.DB.Abonnements.SetLastEntry(abonnement.Feed.Url, lastEntry)
			}
		}()
	}

	wg.Wait()
	log.Println("/===============================")
	time.AfterFunc(1*time.Minute, h.OnCheck)
}

func processContent(content string, replacements *[]storage.Replacement) string {
	processed := html.UnescapeString(content)

	for _, replacement := range *replacements {
		if replacement.IsRegex {
			re := regexp.MustCompile(replacement.Value)
			processed = re.ReplaceAllString(processed, "$1$1")
		} else {
			processed = strings.ReplaceAll(processed, replacement.Value, "")
		}
	}

	processed = strings.TrimSpace(processed)

	if len(processed) > 270 {
		return processed[:270] + "..."
	}

	return processed
}

func (h Handler) sendText(chatId int64, text string, url string) error {
	_, err := h.Bot.Send(telebot.ChatID(chatId), text, defaultSendOptions)
	if err != nil {
		if errors.As(err, &telebot.FloodError{}) {
			log.Printf("%s: Flood error, retrying after: %d seconds", url,
				err.(telebot.FloodError).RetryAfter)
			time.Sleep(time.Duration(err.(telebot.FloodError).RetryAfter) * time.Second)
			h.sendText(chatId, text, url)
		} else {
			return err
		}

	}
	return nil
}
