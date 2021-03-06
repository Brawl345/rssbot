package main

import (
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Brawl345/rssbot/config"
	"github.com/Brawl345/rssbot/handler"
	_ "github.com/joho/godotenv/autoload"
	"gopkg.in/telebot.v3"
	"gopkg.in/telebot.v3/middleware"

	"log"
	"os"

	"github.com/Brawl345/rssbot/storage"
)

func main() {
	tmpl, err := config.GetTemplate("post.gohtml")
	if err != nil {
		log.Fatal("Invalid template: ", err)
	}

	cfg := &config.Config{
		Template: tmpl,
	}

	db, err := storage.Connect()
	if err != nil {
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
		Bot:    bot,
		Config: cfg,
		DB:     db,
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

	bot.Handle("/repl_add", h.OnAddReplacement)
	bot.Handle("/repl_add_re", h.OnAddRegexReplacement)
	bot.Handle("/repl_list", h.OnListReplacements)
	bot.Handle("/repl_del", h.OnDeleteReplacement)

	time.AfterFunc(5*time.Second, h.OnCheck)

	channel := make(chan os.Signal)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM)
	signal.Notify(channel, os.Interrupt, syscall.SIGKILL)
	signal.Notify(channel, os.Interrupt, syscall.SIGINT)
	go func() {
		<-channel
		log.Println("Stopping...")
		bot.Stop()
		err := db.Close()
		if err != nil {
			log.Println(err)
			os.Exit(1)
			return
		}
		os.Exit(0)
	}()

	bot.Start()
}
