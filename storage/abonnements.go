package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

type (
	AbonnementStorage interface {
		Create(chatId int64, chatTitle string, feedUrl string, lastEntry, etag, lastModified *string, nextPollAt time.Time) error
		Delete(chatId int64, feedId int64) error
		ExistsByFeedUrl(chatId int64, feedUrl string) (bool, error)
		ExistsById(chatId int64, feedId int64) (bool, error)
		GetByUser(chatId int64) ([]Feed, error)
		GetAll() ([]Abonnement, error)
		GetDue() ([]Abonnement, error)
		SetFeedState(feedID int64, lastEntry, etag, lastModified *string, nextPollAt time.Time, errorCount, unchangedCount int) error
		Reschedule(feedID int64, nextPollAt time.Time, errorCount, unchangedCount int) error
		MoveFeedURL(feedID int64, newURL string) (bool, error)
		DisableFeed(feedID int64, reason string) error
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
		Title     string    `db:"title"`
		CreatedAt time.Time `db:"chat_created_at"`
	}

	Feed struct {
		ID             int64          `db:"id"`
		Url            string         `db:"url"`
		LastEntry      sql.NullString `db:"last_entry"`
		CreatedAt      time.Time      `db:"created_at"`
		UpdatedAt      sql.NullTime   `db:"updated_at"`
		ETag           sql.NullString `db:"etag"`
		LastModified   sql.NullString `db:"last_modified"`
		NextPollAt     sql.NullTime   `db:"next_poll_at"`
		LastPollAt     sql.NullTime   `db:"last_poll_at"`
		ErrorCount     int            `db:"error_count"`
		UnchangedCount int            `db:"unchanged_count"`
		Disabled       bool           `db:"disabled"`
		DisabledReason sql.NullString `db:"disabled_reason"`
	}
)

func (db *Abonnements) Create(chatId int64, chatTitle string, feedUrl string, lastEntry, etag, lastModified *string, nextPollAt time.Time) error {
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
		const insertFeedQuery = "INSERT INTO feeds (url, last_entry, etag, last_modified, next_poll_at) VALUES (?, ?, ?, ?, ?)"
		result, err := tx.Exec(insertFeedQuery, feedUrl, lastEntry, etag, lastModified, nextPollAt)
		if err != nil {
			return err
		}

		feedId, _ = result.LastInsertId()
	}

	const insertChatQuery = "INSERT INTO chats (id, title) VALUES (?, ?) ON DUPLICATE KEY UPDATE title = ?"
	_, err = tx.Exec(insertChatQuery, chatId, chatTitle, chatTitle)

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

// abonnementSelect lists feed columns explicitly so the manual row scan does
// not depend on the physical column order of `feeds.*`.
const abonnementSelect = `SELECT chats.id, chats.created_at, chats.title,
feeds.id, feeds.url, feeds.last_entry, feeds.created_at, feeds.updated_at,
feeds.etag, feeds.last_modified, feeds.next_poll_at, feeds.last_poll_at,
feeds.error_count, feeds.unchanged_count, feeds.disabled, feeds.disabled_reason
FROM abonnements
JOIN chats ON abonnements.chat_id = chats.id
JOIN feeds ON abonnements.feed_id = feeds.id`

func (db *Abonnements) GetAll() ([]Abonnement, error) {
	rows, err := db.Queryx(abonnementSelect)
	if err != nil {
		return nil, err
	}
	return scanAbonnements(rows)
}

// GetDue returns only feeds that are enabled and whose scheduled poll time has
// passed (or was never set). This is what keeps polling on a per-feed schedule
// and prevents a process restart from re-downloading everything (FRB037).
// Timestamps are always passed from Go instead of using NOW(), so they are
// written and compared in the driver's loc regardless of the MySQL time zone.
func (db *Abonnements) GetDue() ([]Abonnement, error) {
	const where = ` WHERE feeds.disabled = 0 AND (feeds.next_poll_at IS NULL OR feeds.next_poll_at <= ?)`
	rows, err := db.Queryx(abonnementSelect+where, time.Now())
	if err != nil {
		return nil, err
	}
	return scanAbonnements(rows)
}

func scanAbonnements(rows *sqlx.Rows) ([]Abonnement, error) {
	defer rows.Close()

	feeds := make(map[int64]Feed)
	feedChats := make(map[int64][]Chat)
	var order []int64

	for rows.Next() {
		var chat Chat
		var feed Feed
		err := rows.Scan(&chat.ID, &chat.CreatedAt, &chat.Title,
			&feed.ID, &feed.Url, &feed.LastEntry, &feed.CreatedAt, &feed.UpdatedAt,
			&feed.ETag, &feed.LastModified, &feed.NextPollAt, &feed.LastPollAt,
			&feed.ErrorCount, &feed.UnchangedCount, &feed.Disabled, &feed.DisabledReason)
		if err != nil {
			return nil, err
		}

		if _, seen := feeds[feed.ID]; !seen {
			order = append(order, feed.ID)
		}
		feeds[feed.ID] = feed
		feedChats[feed.ID] = append(feedChats[feed.ID], chat)
	}

	var abonnements []Abonnement
	for _, feedId := range order {
		abonnements = append(abonnements, Abonnement{
			Feed:  feeds[feedId],
			Chats: feedChats[feedId],
		})
	}

	return abonnements, rows.Err()
}

// SetFeedState writes the atomic cache set (etag + last_modified), the last seen
// entry and the next poll schedule after a successful 200 response.
func (db *Abonnements) SetFeedState(feedID int64, lastEntry, etag, lastModified *string, nextPollAt time.Time, errorCount, unchangedCount int) error {
	const query = `UPDATE feeds
SET last_entry = ?, etag = ?, last_modified = ?, next_poll_at = ?, last_poll_at = ?,
    error_count = ?, unchanged_count = ?
WHERE id = ?`
	_, err := db.Exec(query, lastEntry, etag, lastModified, nextPollAt, time.Now(), errorCount, unchangedCount, feedID)
	return err
}

// Reschedule updates only the poll schedule and counters, preserving the cached
// etag/last_modified (FRB010-016) — used for 304, rate-limiting and transient
// errors.
func (db *Abonnements) Reschedule(feedID int64, nextPollAt time.Time, errorCount, unchangedCount int) error {
	const query = `UPDATE feeds
SET next_poll_at = ?, last_poll_at = ?, error_count = ?, unchanged_count = ?
WHERE id = ?`
	_, err := db.Exec(query, nextPollAt, time.Now(), errorCount, unchangedCount, feedID)
	return err
}

// MoveFeedURL persists a permanent redirect target (FRB130/131). If another feed
// already occupies newURL (feeds.url is UNIQUE), this feed's subscriptions are
// merged onto that existing feed instead and the old feed row is removed; the
// returned bool reports whether such a merge happened.
func (db *Abonnements) MoveFeedURL(feedID int64, newURL string) (bool, error) {
	tx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var targetID int64
	err = tx.Get(&targetID, "SELECT id FROM feeds WHERE url = ?", newURL)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec("UPDATE feeds SET url = ? WHERE id = ?", newURL, feedID); err != nil {
			return false, err
		}
		return false, tx.Commit()
	case err != nil:
		return false, err
	case targetID == feedID:
		return false, tx.Commit()
	}

	// Repoint subscriptions onto the existing feed, dropping duplicates for
	// chats already subscribed there, then delete the now-orphaned feed.
	if _, err := tx.Exec("UPDATE IGNORE abonnements SET feed_id = ? WHERE feed_id = ?", targetID, feedID); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM abonnements WHERE feed_id = ?", feedID); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM feeds WHERE id = ?", feedID); err != nil {
		return false, err
	}
	// Let the surviving feed pick up the merged subscribers on the next tick.
	if _, err := tx.Exec("UPDATE feeds SET next_poll_at = ? WHERE id = ? AND disabled = 0", time.Now(), targetID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// DisableFeed retires a feed that has gone away (FRB110-118).
func (db *Abonnements) DisableFeed(feedID int64, reason string) error {
	const query = `UPDATE feeds SET disabled = 1, disabled_reason = ?, next_poll_at = NULL WHERE id = ?`
	_, err := db.Exec(query, reason, feedID)
	return err
}
