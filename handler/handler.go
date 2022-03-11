package handler

import (
	"github.com/Brawl345/rssbot/config"
	"github.com/Brawl345/rssbot/storage"
	"gopkg.in/telebot.v3"
)

type Handler struct {
	Bot    *telebot.Bot
	Config *config.Config
	DB     *storage.DB
}
