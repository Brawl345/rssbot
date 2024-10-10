package handler

import (
	"bytes"
	"errors"
	"html"
	"log"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Brawl345/rssbot/storage"
	"gopkg.in/telebot.v3"
)

type TemplateData struct {
	Title      string
	FeedTitle  string
	Content    string
	PostLink   string
	PostDomain string
}

func (h *Handler) OnCheck() {
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
				templateData := &TemplateData{}
				if entry.Title != "" {
					templateData.Title = html.EscapeString(entry.Title)
				} else {
					templateData.Title = "Kein Titel"
				}

				templateData.FeedTitle = html.EscapeString(feed.Title)

				if entry.Content != "" {
					templateData.Content = processContent(entry.Content, &replacements)
				} else if entry.Description != "" {
					templateData.Content = processContent(entry.Description, &replacements)
				}

				if entry.Link != "" {
					templateData.PostLink = entry.Link
				} else {
					templateData.PostLink = feed.Link
				}

				re := regexp.MustCompile("^https?://feedproxy.google.com/~r/(.+?)/.*")
				match := re.FindStringSubmatch(templateData.PostLink)

				if len(match) > 0 {
					templateData.PostDomain = match[1]
				} else {
					parsedUrl, _ := url.Parse(templateData.PostLink)
					templateData.PostDomain = parsedUrl.Host
				}

				templateData.PostDomain = strings.Replace(templateData.PostDomain, "www.", "", 1)

				var tpl bytes.Buffer
				err := h.Config.Template.Execute(&tpl, templateData)
				if err != nil {
					log.Printf("%s: %s", abonnement.Feed.Url, err)
					return
				}

				for _, chat := range abonnement.Chats {
					err = h.sendText(chat.ID, tpl.String(), abonnement.Feed.Url)
					if err != nil {
						log.Printf("%s: %s", abonnement.Feed.Url, err)
					}
				}

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
			h.sendText(chatId, text, url)
		} else {
			return err
		}

	}
	return nil
}
