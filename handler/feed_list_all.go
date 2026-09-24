package handler

import (
	"fmt"
	"html"
	"log"
	"strings"

	"gopkg.in/telebot.v3"
)

func (h *Handler) OnListAll(c telebot.Context) error {
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
		fmt.Fprintf(&sb, "<b>%d)</b> %s%s\n", abonnement.ID, html.EscapeString(abonnement.Url), feedStatus(abonnement.Feed))

		for _, chat := range abonnement.Chats {
			fmt.Fprintf(&sb, "    <code>%d</code> (%s)\n", chat.ID, html.EscapeString(chat.Title))
		}

		sb.WriteString("\n")
	}

	return c.Send(sb.String(), defaultSendOptions)
}
