# RSS Bot for Telegram

RSS Bot for Telegram written in Go. Uses a MySQL database to store the latest entries.

Only one user (the "admin") can manage the bot, but it's possible to let the bot post into channels.

The bot's language is German, but it should be self-explanatory.

## Features
* Every feed has its own schedule, see [How polling works](#how-polling-works)
* Polite fetching: conditional requests, compression, backoff on errors and rate limits
* Slows down feeds that rarely change (optional)
* Can post private, in channels or groups
* Custom post format with a `post.gohtml` file
* Supports "replacements" where specific words will be removed (limited Regex is also supported). This is useful for spam like "Read more on XYZ" and stuff

## Usage

1. Download binary for your system from Releases or build it yourself
2. Copy ".env.example" to ".env" and fill it in
3. (Optional) Create a `post.gohtml` with a custom Go HTML template that will be used for posts (see below)
4. Run and done! Database migrations are applied automatically.

## How polling works

The bot does not fetch all feeds at once. Every feed has its own "next poll" time stored in the database. Every `POLL_TICK` (default: 30 seconds) the bot fetches the feeds that are due and then calculates their next poll time. Because the schedule lives in the database, restarting the bot does not trigger a re-download of all feeds.

Feeds are fetched in parallel (up to `POLL_CONCURRENCY`), but feeds on the same host are fetched one after another so a single server is never hit with several requests at once.

Each request sends the `ETag` and `Last-Modified` values from the previous response. If nothing changed, the server can answer with a tiny `304 Not Modified` instead of the whole feed. Responses are compressed when the server supports it.

### When is a feed polled next?

1. **Base interval:** `POLL_INTERVAL` (default: 10 minutes).
2. **Adaptive slow-down** (`POLL_ADAPTIVE`, on by default): every poll without a new entry adds one base interval to the wait time, up to `POLL_INTERVAL_MAX` (default: 6 hours). As soon as a new entry shows up, the feed is back to the base interval.

   | Polls without new entries | Wait until next poll (defaults) |
   |---------------------------|---------------------------------|
   | 0                         | 10 min                          |
   | 1                         | 20 min                          |
   | 2                         | 30 min                          |
   | 9 (≈ 8 hours of silence)  | 1 h 40 min                      |
   | 35 (≈ 4 days of silence)  | 6 h (maximum)                   |

   So a feed that was quiet over night may take up to ~1 h 40 min to deliver its first new post in the morning, while active feeds stay at 10 minutes.

   **With `POLL_ADAPTIVE=false`** every feed is polled every `POLL_INTERVAL`, no matter how often it changes. New posts arrive faster, but the bot sends more requests. Thanks to the conditional requests, most of them are cheap `304` answers.
3. **Server hints:** if the server asks for a longer interval (`Cache-Control: max-age` header or `<ttl>` in an RSS feed), the bot waits at least that long, but never longer than `POLL_INTERVAL_MAX`. Hints can only slow polling down, never speed it up.
4. **Quiet hours:** RSS `<skipHours>` and `<skipDays>` (in UTC) are respected by moving the next poll out of these times.

The actual time can be up to `POLL_TICK` later than calculated.

### Errors, rate limits and moved feeds

* **Temporary errors** (timeouts, HTTP 404/500, invalid feed, …): the wait time doubles with every consecutive failure (10 min, 20 min, 40 min, …, up to `POLL_INTERVAL_MAX`). A feed is **disabled** after it failed at least 12 times in a row *and* has been failing for 7 days.
* **HTTP 410 Gone:** the feed is disabled immediately.
* **HTTP 429/503:** the bot waits as long as the `Retry-After` header says (or 4 × `POLL_INTERVAL` without it, capped at `POLL_INTERVAL_MAX`). This does not count as an error.
* **Permanent redirects (301/308):** the new URL is saved automatically. If another subscription already uses the new URL, both are merged. Temporary redirects (302/307) are only followed.
* Redirects from a public feed to a private/local network address are refused.

The admin gets a private message when a feed is disabled, moved or rate limited. Subscribed channels and groups only ever receive feed entries.

Disabled feeds are marked with 🚫 in `/rss`. To enable one again, simply subscribe to it again with `/sub`.

### Use your own template

The bot reads the `post.gohtml` from the same directory and uses it as a [Go template](https://pkg.go.dev/text/template) where it inserts the data. Take a look inside the [handler/feed_check.go](handler/feed_check.go) file (the `TemplateData` struct) to see all available fields. You can find the default template inside the [config/config.go](config/config.go) file. [Limited HTML](https://core.telegram.org/bots/api#html-style) is supported and all fields are sanitized with HTML tags removed and "replacements" applied. 

Example:

```gohtml
<b>[#RSS] {{.Title}}</b>
<i>{{.FeedTitle}}</i>
{{- if ne .Content "" }}
{{.Content}}
{{- end }}
<a href="{{.PostLink}}">{{.PostDomain}}</a>
```
