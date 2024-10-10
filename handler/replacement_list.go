package handler

import (
	"fmt"
	"html"
	"log"
	"strings"

	"gopkg.in/telebot.v3"
)

func (h *Handler) OnListReplacements(c telebot.Context) error {
	replacements, err := h.DB.Replacements.List()

	if !c.Message().Private() {
		// Block command in chats to avoid leaking information
		return nil
	}

	if err != nil {
		log.Println(err)
		return c.Send("❌ Beim Abrufen der Ersetzungen ist ein Fehler aufgetreten.", defaultSendOptions)
	}

	if len(replacements) == 0 {
		return c.Send("Es wurden noch keine Ersetzungen angelegt.", defaultSendOptions)
	}

	sb := strings.Builder{}

	for _, replacement := range replacements {
		sb.WriteString(fmt.Sprintf("<b>%d)</b> <code>%s</code>", replacement.ID, html.EscapeString(replacement.Value)))
		if replacement.IsRegex {
			sb.WriteString(" <i>(RegEx)</i>")
		}
		sb.WriteString("\n")
	}

	return c.Send(sb.String(), defaultSendOptions)

}
