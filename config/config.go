package config

import (
	"os"
	"text/template"
)

type Config struct {
	Template *template.Template
}

func fileExists(fileName string) bool {
	if _, err := os.Stat(fileName); err == nil {
		return true
	}
	return false
}

func GetTemplate(path string) (*template.Template, error) {
	if fileExists(path) {
		return template.ParseFiles(path)
	} else {
		return template.New("post").Parse(`<b>{{.Title}}</b>
<i>{{.FeedTitle}}</i>
{{- if ne .Content "" }}
{{.Content}}
{{- end }}
<a href="{{.PostLink}}">Weiterlesen auf {{.PostDomain}}</a>`)
	}
}
