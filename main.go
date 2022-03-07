package main

import (
	"github.com/Brawl345/rssbot/handler"
	_ "github.com/joho/godotenv/autoload"
	"gopkg.in/telebot.v3"
	"gopkg.in/telebot.v3/middleware"
	"strconv"
	"time"

	"github.com/Brawl345/rssbot/storage"
	"log"
	"os"
)

func main() {
	db, err := storage.Open(os.Getenv("MYSQL_URL"))
	if err != nil {
		log.Fatal(err)
	}

	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	log.Println("Database connection established")

	n, err := db.Migrate()
	if err != nil {
		log.Fatalln(err)
	}
	if n > 0 {
		log.Printf("Applied %d migration(s)", n)
	}

	pref := telebot.Settings{
		Token:  os.Getenv("BOT_TOKEN"),
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	}

	bot, err := telebot.NewBot(pref)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Logged in as @%s (%d)", bot.Me.Username, bot.Me.ID)

	h := handler.Handler{
		Bot: bot,
		DB:  db,
	}

	adminId, err := strconv.ParseInt(os.Getenv("ADMIN_ID"), 10, 64)

	if err != nil {
		// No admin = unsupported.
		// Else we would constantly have to check if the user is also
		// a member of the channel if feeds should be posted in a channel.
		log.Fatalln("ADMIN_ID is missing.")
	} else {
		bot.Use(middleware.Whitelist(adminId))
	}

	bot.Handle("/start", h.OnStart)
	bot.Handle("/help", h.OnStart)

	bot.Handle("/sub", h.OnSubscribe)

	bot.Handle("/del", h.OnUnsubscribe)
	bot.Handle("/unsub", h.OnUnsubscribe)

	bot.Handle("/feeds", h.OnList)
	bot.Handle("/rss", h.OnList)

	bot.Handle("/feeds_all", h.OnListAll)
	bot.Handle("/rss_all", h.OnListAll)

	time.AfterFunc(10*time.Second, h.OnCheck)

	bot.Start()
}
