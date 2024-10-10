package handler

import (
	"strings"

	"gopkg.in/telebot.v3"
)

func (h *Handler) OnStart(c telebot.Context) error {
	var sb strings.Builder
	sb.WriteString("<b>/rss</b> <i>[Chat]</i>: Abonnierte Feeds anzeigen\n")
	sb.WriteString("<b>/rss_all</b>: Alle abonnierten Feeds aus jedem Chat anzeigen\n")
	sb.WriteString("<b>/sub</b> <i>Feed-URL</i> <i>[Chat]</i>: Feed abonnieren\n")
	sb.WriteString("<b>/del</b> <i>Feed-ID</i> <i>[Chat]</i>: Feed deabonnieren\n\n")
	sb.WriteString("<b>/repl_list</b>: Ersetzungen anzeigen\n")
	sb.WriteString("<b>/repl_add</b> <i>String</i>: Ersetzung hinzufügen\n")
	sb.WriteString("<b>/repl_add_re</b> <i>RegEx</i>: RegEx-Ersetzung hinzufügen\n")
	sb.WriteString("<b>/repl_del</b> <i>Ersetzungs-ID</i>: Ersetzung löschen\n\n")
	sb.WriteString("<i>[Chat]</i> ist ein optionales Argument mit dem <code>@Kanalnamen</code>.")

	return c.Send(sb.String(), defaultSendOptions)
}
