package handler

import (
	"fmt"
	"gopkg.in/telebot.v3"
	"html"
	"log"
	"strings"
)

func (h Handler) OnList(c telebot.Context) error {
	args := c.Args()

	if len(args) > 1 {
		return nil
	}

	chatId := c.Chat().ID
	var chatTitle string
	if c.Message().Private() {
		chatTitle = c.Chat().FirstName
	} else {
		chatTitle = c.Chat().Title
	}

	if len(args) == 1 {
		// Chat ID given
		chatInfo, err := h.Bot.ChatByUsername(args[0])
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

	links, err := h.DB.Abonnements.GetByUser(chatId)

	if err != nil {
		log.Println(err)
		return c.Send("Feed-Liste konnte nicht abgerufen werden.", defaultSendOptions)
	}

	if len(links) == 0 {
		return c.Send("❌ Keine Feeds abonniert.", defaultSendOptions)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<b>%s</b> hat abonniert:\n", html.EscapeString(chatTitle)))

	for _, link := range links {
		sb.WriteString(fmt.Sprintf("<b>%d)</b> %s\n", link.ID, html.EscapeString(link.Url)))
	}

	return c.Send(sb.String(), defaultSendOptions)
}
