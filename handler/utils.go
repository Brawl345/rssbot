package handler

import (
	"github.com/mmcdole/gofeed"
	"gopkg.in/telebot.v3"
	"html"
	"regexp"
	"strings"
)

var defaultSendOptions = &telebot.SendOptions{
	AllowWithoutReply:     true,
	DisableWebPagePreview: true,
	ParseMode:             telebot.ModeHTML,
}

var replacements = [35]string{
	"[←]",
	"[…]",
	"[...]",
	"[bilder]",
	"[boerse]",
	"[mehr]",
	"[video]",
	"...[more]",
	"[more]",
	"[liveticker]",
	"[livestream]",
	"[multimedia]",
	"[sportschau]",
	"[phoenix]",
	"[swr]",
	"[ndr]",
	"[mdr]",
	"[rbb]",
	"[wdr]",
	"[hr]",
	"[br]",
	"Click for full.",
	"Read more »",
	"Read more",
	"...Read More",
	"...mehr lesen",
	"mehr lesen",
	"(more…)",
	"View On WordPress",
	"Continue reading →",
	"» weiterlesen",
	"(Feed generated with  FetchRSS)",
	"(RSS generated with  FetchRss)",
	"-- Delivered by Feed43 service",
	"Meldung bei www.tagesschau.de lesen",
}

var regexReplacements = [4]*regexp.Regexp{
	regexp.MustCompile(`Der Beitrag.*erschien zuerst auf .+.`),
	regexp.MustCompile(`The post.*appeared first on .+.`),
	regexp.MustCompile(`http://www.serienjunkies.de/.*.html`),
	regexp.MustCompile(`<.*?>`),
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

func processContent(content string) string {
	re := regexp.MustCompile(`<.*?>`)
	processed := html.UnescapeString(content)
	processed = re.ReplaceAllString(processed, "$1$1")

	for _, replacement := range replacements {
		processed = strings.ReplaceAll(processed, replacement, "")
	}

	for _, replacement := range regexReplacements {
		processed = replacement.ReplaceAllString(processed, "$1$1")
	}

	processed = strings.TrimSpace(processed)

	if len(processed) > 270 {
		return processed[:270] + "..."
	}

	return processed
}
