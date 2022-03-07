package handler

import (
	"gopkg.in/telebot.v3"
	"strings"
)

func (h Handler) OnStart(c telebot.Context) error {
	var sb strings.Builder
	sb.WriteString("<b>/rss</b> <i>[Chat]</i>: Abonnierte Feeds anzeigen\n")
	sb.WriteString("<b>/sub</b> <i>Feed-URL</i> <i>[Chat]</i>: Feed abonnieren\n")
	sb.WriteString("<b>/del</b> <i>Feed-URL</i> <i>[Chat]</i>: Feed löschen\n")
	sb.WriteString("<i>[Chat]</i> ist ein optionales Argument mit dem @Kanalnamen.")

	return c.Send(sb.String(), defaultSendOptions)
}
