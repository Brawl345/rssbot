package handler

import (
	"github.com/go-sql-driver/mysql"
	"gopkg.in/telebot.v3"
	"log"
	"regexp"
	"strings"
)

func (h *Handler) OnAddReplacement(c telebot.Context) error {
	args := c.Args()

	if len(args) == 0 {
		return nil
	}

	replacement := strings.Join(args, " ")

	err := h.DB.Replacements.Create(replacement, false)

	if err != nil {
		if mysqlError, ok := err.(*mysql.MySQLError); ok {
			if mysqlError.Number == 1062 { // https://mariadb.com/kb/en/mariadb-error-codes/
				return c.Send("✅ Die Ersetzung wurde bereits hinzugefügt.", defaultSendOptions)
			}
		}

		log.Println(err)
		return c.Send("❌ Beim Anlegen der Ersetzung ist ein Fehler aufgetreten.", defaultSendOptions)
	}

	return c.Send("✅ Ersetzung wurde erfolgreich angelegt.", defaultSendOptions)
}

func (h *Handler) OnAddRegexReplacement(c telebot.Context) error {
	args := c.Args()

	if len(args) == 0 {
		return nil
	}

	replacement := strings.Join(args, " ")

	_, err := regexp.Compile(replacement)
	if err != nil {
		log.Println(err)
		return c.Send("❌ Kein gültiges RegEx.", defaultSendOptions)
	}

	err = h.DB.Replacements.Create(replacement, true)

	if err != nil {
		if mysqlError, ok := err.(*mysql.MySQLError); ok {
			if mysqlError.Number == 1062 { // https://mariadb.com/kb/en/mariadb-error-codes/
				return c.Send("✅ Die Ersetzung wurde bereits hinzugefügt.", defaultSendOptions)
			}
		}

		log.Println(err)
		return c.Send("❌ Beim Anlegen der Ersetzung ist ein Fehler aufgetreten.", defaultSendOptions)
	}

	return c.Send("✅ Ersetzung wurde erfolgreich angelegt.", defaultSendOptions)
}
