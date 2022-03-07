# RSS bot for Telegram

RSS bot for Telegram written in Go. Uses a MySQL database to store the latest entries.

Only one user (the "admin") can manage the bot, but it's possible to let the bot post into channels.

The bot's language is German, but it should be self-explanatory.

## Usage

1. Download binary for your system from Releases or build it yourself
2. Copy ".env.example" to ".env" and fill it in
3. Run and done! Database migrations are applied automatically.

Feeds are checked every minute after the latest check finished (it waits for ten seconds the first time after the bot starts).
