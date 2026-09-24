package handler

import (
	"fmt"
	"html"

	"github.com/Brawl345/rssbot/storage"
	"github.com/mmcdole/gofeed"
	"gopkg.in/telebot.v3"
)

var defaultSendOptions = &telebot.SendOptions{
	AllowWithoutReply:     true,
	DisableWebPagePreview: true,
	ParseMode:             telebot.ModeHTML,
}

func reverse(s []*gofeed.Item) []*gofeed.Item {
	a := make([]*gofeed.Item, len(s))
	copy(a, s)

	for i := len(a)/2 - 1; i >= 0; i-- {
		opp := len(a) - 1 - i
		a[i], a[opp] = a[opp], a[i]
	}

	return a
}

func feedStatus(feed storage.Feed) string {
	if !feed.Disabled {
		return ""
	}
	return fmt.Sprintf(" 🚫 <i>deaktiviert: %s</i>", html.EscapeString(feed.DisabledReason.String))
}
