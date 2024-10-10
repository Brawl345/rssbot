package handler

import (
	"log"

	"github.com/mmcdole/gofeed"
	"gopkg.in/telebot.v3"
)

func (h *Handler) OnSubscribe(c telebot.Context) error {
	args := c.Args()

	if len(args) == 0 || len(args) > 2 {
		return nil
	}

	feedUrl := args[0]
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

	feed, err := gofeed.NewParser().ParseURL(feedUrl)

	if err != nil {
		log.Println(err)
		return c.Send("❌ Ungültiger Feed", defaultSendOptions)
	}

	if feed.FeedLink != "" {
		feedUrl = feed.FeedLink
	}

	exists, _ := h.DB.Abonnements.ExistsByFeedUrl(chatId, feedUrl)

	if exists {
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

	err = h.DB.Abonnements.Create(chatId, chatTitle, feedUrl, lastEntry)
	if err != nil {
		log.Println(err)
		return c.Send("❌ Beim Abonnieren des Feeds ist ein Fehler aufgetreten.", defaultSendOptions)
	}

	return c.Send("✅ Der Feed wurde erfolgreich abonniert!", defaultSendOptions)
}
