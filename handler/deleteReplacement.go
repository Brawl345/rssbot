package handler

import (
	"gopkg.in/telebot.v3"
	"log"
	"strconv"
)

func (h Handler) OnDeleteReplacement(c telebot.Context) error {
	args := c.Args()

	if len(args) == 0 || len(args) > 1 {
		return nil
	}

	replacementId, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return c.Send("❌ Bitte die zu löschende Ersetzung-ID angeben.", defaultSendOptions)
	}

	err = h.DB.Replacements.Delete(replacementId)
	if err != nil {
		log.Println(err)
		return c.Send("❌ Diese Ersetzung wurde nicht gefunden.", defaultSendOptions)
	}

	return c.Send("✅ Ersetzung gelöscht.", defaultSendOptions)
}
