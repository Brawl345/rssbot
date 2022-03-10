package handler

import (
	"fmt"
	"gopkg.in/telebot.v3"
	"html"
	"log"
	"strings"
)

func (h Handler) OnListAll(c telebot.Context) error {
	if !c.Message().Private() {
		return nil
	}

	abonnements, err := h.DB.Abonnements.GetAll()
	if err != nil {
		log.Println(err)
		return c.Send("❌ Beim Abrufen aller Feeds ist ein Fehler aufgetreten.", defaultSendOptions)
	}

	if len(abonnements) == 0 {
		return c.Send("Es wurden noch keine Feeds abonniert.", defaultSendOptions)
	}

	sb := strings.Builder{}

	for _, abonnement := range abonnements {
		sb.WriteString(fmt.Sprintf("<b>%d)</b> %s\n", abonnement.Feed.ID, abonnement.Feed.Url))

		for _, chat := range abonnement.Chats {
			sb.WriteString(fmt.Sprintf("    <code>%d</code> (%s)\n", chat.ID,
				html.EscapeString(chat.Title)))
		}

		sb.WriteString("\n")
	}

	return c.Send(sb.String(), defaultSendOptions)
}
