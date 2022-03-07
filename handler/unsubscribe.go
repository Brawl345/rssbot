package handler

import (
	"gopkg.in/telebot.v3"
	"log"
	"strconv"
)

func (h Handler) OnUnsubscribe(c telebot.Context) error {
	args := c.Args()

	if len(args) == 0 || len(args) > 2 {
		return nil
	}

	feedId, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return c.Send("❌ Bitte die zu löschende Feed-ID angeben.")
	}

	chatId := c.Chat().ID
	if len(args) == 2 {
		// Chat ID given
		chatInfo, err := h.Bot.ChatByUsername(args[1])
		if err != nil {
			return c.Send("❌ Dieser Kanal existiert nicht.")
		}

		userInfo, err := h.Bot.ChatMemberOf(chatInfo, h.Bot.Me)
		if err != nil {
			return c.Send("❌ Dieser Kanal existiert nicht.")
		}

		if userInfo.Role != telebot.Administrator || !userInfo.CanPostMessages {
			return c.Send("❌ Du musst den Bot als Administrator zu diesem Kanal hinzufügen und/oder die Berechtigung zum Posten erteilen.")
		}

		chatId = chatInfo.ID
	}

	exists, _ := h.DB.Abonnements.ExistsById(chatId, feedId)

	if !exists {
		return c.Send("❌ Dieser Feed wurde nicht abonniert.", defaultSendOptions)
	}

	err = h.DB.Abonnements.Delete(chatId, feedId)
	if err != nil {
		log.Println(err)
		return c.Send("❌ Beim Deabonnieren ist ein Fehler aufgetreten.")
	}

	return c.Send("✅ Der Feed wurde deabonniert.")

}
