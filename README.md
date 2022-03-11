# RSS Bot for Telegram

RSS Bot for Telegram written in Go. Uses a MySQL database to store the latest entries.

Only one user (the "admin") can manage the bot, but it's possible to let the bot post into channels.

The bot's language is German, but it should be self-explanatory.

## Features
* Checks feeds every minute (after all checks are finished)
* Concurrent checks
* Can post private, in channels or groups
* Custom post format with a `post.gohtml` file
* Supports "replacements" where specific words will be removed (limited Regex is also supported). This is useful for spam like "Read more on XYZ" and stuff

## Usage

1. Download binary for your system from Releases or build it yourself
2. Copy ".env.example" to ".env" and fill it in
3. (Optional) Create a `post.gohtml` with a custom Go HTML template that will be used for posts (see below)
4. Run and done! Database migrations are applied automatically.

Feeds are checked every minute after the latest check finished (it waits for five seconds the first time after the bot starts).

### Use your own template

The bot reads the `post.gohtml` from the same directory and uses it as a [Go template](https://pkg.go.dev/text/template) where it inserts the data. Take a look inside the [handler/feed_check.go](handler/feed_check.go) file (the `TemplateData` struct) to see all available fields. You can find the default template inside the [config/config.go](config/config.go) file. [Limited HTML](https://core.telegram.org/bots/api#html-style) is supported and all fields are sanitized with HTML fields removed and "replacements" applied. 

Example:

```gohtml
<b>[#RSS] {{.Title}}</b>
<i>{{.FeedTitle}}</i>
{{- if ne .Content "" }}
{{.Content}}
{{- end }}
<a href="{{.PostLink}}">{{.PostDomain}}</a>
```
