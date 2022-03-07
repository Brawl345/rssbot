package storage

import (
	"context"
	"github.com/jmoiron/sqlx"
	"time"
)

type (
	AbonnementStorage interface {
		Create(chatId int64, feedUrl string, lastEntry *string) error
		Delete(chatId int64, feedId int64) error
		ExistsByFeedUrl(chatId int64, feedUrl string) (bool, error)
		ExistsById(chatId int64, feedId int64) (bool, error)
		GetByUser(chatId int64) ([]Feed, error)
		GetAll() ([]Abonnement, error)
		SetLastEntry(feedUrl string, lastEntry *string) error
	}

	Abonnements struct {
		*sqlx.DB
	}

	Abonnement struct {
		Feed
		Chats []Chat
	}

	Chat struct {
		ID        int64     `db:"chat_id"`
		CreatedAt time.Time `db:"chat_created_at"`
	}
)

func (db *Abonnements) Create(chatId int64, feedUrl string, lastEntry *string) error {
	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}

	defer tx.Rollback()

	const feedQuery = "SELECT id FROM feeds WHERE url = ?"
	var feedId int64
	err = tx.Get(&feedId, feedQuery, feedUrl)

	if err != nil {
		// Feed does not exist yet, will be created
		const insertFeedQuery = "INSERT INTO feeds (url, last_entry) VALUES (?, ?)"
		result, err := tx.Exec(insertFeedQuery, feedUrl, lastEntry)
		if err != nil {
			return err
		}

		feedId, _ = result.LastInsertId()
	}

	const insertChatQuery = "INSERT INTO chats (id) VALUES (?) ON DUPLICATE KEY UPDATE id = id"
	_, err = tx.Exec(insertChatQuery, chatId)

	if err != nil {
		return err
	}

	const insertAbonnementQuery = "INSERT INTO abonnements (chat_id, feed_id) VALUES (?, ?)"
	_, err = tx.Exec(insertAbonnementQuery, chatId, feedId)

	if err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (db *Abonnements) Delete(chatId int64, feedId int64) error {
	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}

	defer tx.Rollback()

	const deleteAbonnementQuery = "DELETE FROM abonnements WHERE abonnements.chat_id = ? AND abonnements.feed_id = ?"
	_, err = tx.Exec(deleteAbonnementQuery, chatId, feedId)
	if err != nil {
		return err
	}

	// Check if user has other abonnements
	const hasOtherAbonnementsQuery = "SELECT 1 FROM abonnements WHERE abonnements.chat_id = ?"
	var hasOtherAbonnements bool
	tx.Get(&hasOtherAbonnements, hasOtherAbonnementsQuery, chatId)

	if !hasOtherAbonnements {
		const deleteChatQuery = "DELETE FROM chats WHERE chats.id = ?"
		_, err = tx.Exec(deleteChatQuery, chatId)
		if err != nil {
			return err
		}
	}

	// Check if feed has abonnement from other users
	const hasOtherUsersQuery = "SELECT 1 FROM abonnements WHERE abonnements.feed_id = ?"
	var hasOtherUsers bool
	tx.Get(&hasOtherUsers, hasOtherUsersQuery, feedId)

	if !hasOtherUsers {
		const deleteFeedQuery = "DELETE FROM feeds WHERE feeds.id = ?"
		_, err = tx.Exec(deleteFeedQuery, feedId)
		if err != nil {
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (db *Abonnements) ExistsByFeedUrl(chatId int64, feedUrl string) (bool, error) {
	const query = `SELECT 1 FROM abonnements
JOIN chats ON abonnements.chat_id = chats.id
JOIN feeds ON abonnements.feed_id = feeds.id
WHERE chats.id = ?
AND feeds.url = ?`

	var exists bool
	err := db.Get(&exists, query, chatId, feedUrl)
	return exists, err
}

func (db *Abonnements) ExistsById(chatId int64, feedId int64) (bool, error) {
	const query = `SELECT 1 FROM abonnements
WHERE abonnements.chat_id = ?
AND abonnements.feed_id = ?`

	var exists bool
	err := db.Get(&exists, query, chatId, feedId)
	return exists, err
}

func (db *Abonnements) GetByUser(chatId int64) ([]Feed, error) {
	const query = `SELECT feeds.* FROM abonnements
JOIN chats ON abonnements.chat_id = chats.id
JOIN feeds ON abonnements.feed_id = feeds.id
WHERE chats.id = ?`

	var feeds []Feed
	err := db.Select(&feeds, query, chatId)
	return feeds, err
}

func (db *Abonnements) GetAll() ([]Abonnement, error) {
	const query = `SELECT chats.id AS "chat_id", chats.created_at AS "chat_created_at", feeds.* FROM abonnements
JOIN chats ON abonnements.chat_id = chats.id
JOIN feeds ON abonnements.feed_id = feeds.id`

	rows, _ := db.Queryx(query)
	defer rows.Close()

	var abonnements []Abonnement
	var feeds = make(map[int64]Feed)
	var feedChats = make(map[int64][]Chat)

	for rows.Next() {
		var chat Chat
		var feed Feed
		rows.Scan(&chat.ID, &chat.CreatedAt, &feed.ID, &feed.Url, &feed.LastEntry, &feed.CreatedAt, &feed.UpdatedAt)

		feeds[feed.ID] = feed

		if chats, ok := feedChats[feed.ID]; ok {
			feedChats[feed.ID] = append(chats, chat)
		} else {
			feedChats[feed.ID] = []Chat{chat}
		}
	}

	for feedId, feed := range feeds {
		abonnements = append(abonnements, Abonnement{
			Feed:  feed,
			Chats: feedChats[feedId],
		})
	}

	return abonnements, nil
}

func (db *Abonnements) SetLastEntry(feedUrl string, lastEntry *string) error {
	const query = `UPDATE feeds
SET feeds.last_entry = ?,
    feeds.updated_at = current_timestamp()
WHERE feeds.url = ?`

	_, err := db.Exec(query, lastEntry, feedUrl)
	return err
}
