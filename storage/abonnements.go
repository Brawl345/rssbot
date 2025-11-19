package storage

import (
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/mmcdole/gofeed"
)

type (
	AbonnementStorage interface {
		Create(chatId int64, chatTitle string, feedUrl string, lastEntry *string) error
		Delete(chatId int64, feedId int64) error
		ExistsByFeedUrl(chatId int64, feedUrl string) (bool, error)
		ExistsById(chatId int64, feedId int64) (bool, error)
		GetByUser(chatId int64) ([]Feed, error)
		GetAll() ([]Abonnement, error)
		SetLastEntry(feedUrl string, lastEntry *string) error
		SetCacheHeaders(feedUrl string, etag *string, lastModified *string) error
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
		ID           int64          `db:"id"`
		Url          string         `db:"url"`
		LastEntry    sql.NullString `db:"last_entry"`
		ETag         sql.NullString `db:"etag"`
		LastModified sql.NullString `db:"last_modified"`
		CreatedAt    time.Time      `db:"created_at"`
		UpdatedAt    sql.NullTime   `db:"updated_at"`
	}

	// FeedCheckResult contains the parsed feed and caching headers
	FeedCheckResult struct {
		Feed         *gofeed.Feed
		ETag         *string
		LastModified *string
	}
)

func (db *Abonnements) Create(chatId int64, chatTitle string, feedUrl string, lastEntry *string) error {
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

func (db *Abonnements) GetAll() ([]Abonnement, error) {
	const query = `SELECT chats.id AS "chat_id", chats.created_at AS "chat_created_at", chats.title, feeds.* 
FROM abonnements
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
		rows.Scan(&chat.ID, &chat.CreatedAt, &chat.Title,
			&feed.ID, &feed.Url, &feed.LastEntry, &feed.CreatedAt, &feed.UpdatedAt)

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
SET feeds.last_entry = ?
WHERE feeds.url = ?`

	_, err := db.Exec(query, lastEntry, feedUrl)
	return err
}

func (db *Abonnements) SetCacheHeaders(feedUrl string, etag *string, lastModified *string) error {
	const query = `UPDATE feeds
SET feeds.etag = ?, feeds.last_modified = ?
WHERE feeds.url = ?`

	_, err := db.Exec(query, etag, lastModified, feedUrl)
	return err
}

// fetchFeedWithCaching fetches a feed using HTTP with smart caching headers
func fetchFeedWithCaching(feedURL string, etag *string, lastModified *string) (*http.Response, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequest("GET", feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set User-Agent to identify the bot
	req.Header.Set("User-Agent", "RSSBot/2.0 (+https://github.com/Brawl345/rssbot)")

	// Enable compression
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	// Add caching headers if we have them
	if etag != nil && *etag != "" {
		req.Header.Set("If-None-Match", *etag)
	}
	if lastModified != nil && *lastModified != "" {
		req.Header.Set("If-Modified-Since", *lastModified)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch feed: %w", err)
	}

	return resp, nil
}

// readResponseBody reads and decompresses the response body if needed
func readResponseBody(resp *http.Response) (io.ReadCloser, error) {
	var reader io.ReadCloser
	var err error

	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
	default:
		reader = resp.Body
	}

	return reader, nil
}

func (feedToCheck Feed) Check(lastEntry *string) (*FeedCheckResult, error) {
	var etag *string
	var lastModified *string

	if feedToCheck.ETag.Valid {
		etag = &feedToCheck.ETag.String
	}
	if feedToCheck.LastModified.Valid {
		lastModified = &feedToCheck.LastModified.String
	}

	resp, err := fetchFeedWithCaching(feedToCheck.Url, etag, lastModified)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Extract new caching headers from response
	var newETag *string
	var newLastModified *string

	if etagHeader := resp.Header.Get("ETag"); etagHeader != "" {
		newETag = &etagHeader
	}
	if lastModHeader := resp.Header.Get("Last-Modified"); lastModHeader != "" {
		newLastModified = &lastModHeader
	}

	// Handle HTTP 304 Not Modified - no new content
	if resp.StatusCode == http.StatusNotModified {
		// Return empty feed with no items to indicate nothing new
		// Keep existing cache headers since server confirmed they're still valid
		return &FeedCheckResult{
			Feed:         &gofeed.Feed{Items: []*gofeed.Item{}},
			ETag:         etag,
			LastModified: lastModified,
		}, nil
	}

	// Handle HTTP 429 Too Many Requests or 503 Service Unavailable
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter != "" {
			return nil, fmt.Errorf("server returned %d, retry after: %s", resp.StatusCode, retryAfter)
		}
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}

	// Handle other non-200 status codes
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read and decompress body if needed
	reader, err := readResponseBody(resp)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	// Parse the feed using gofeed's Parse method
	parser := gofeed.NewParser()
	feed, err := parser.Parse(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse feed: %w", err)
	}

	// Filter out items we've already seen
	if lastEntry != nil {
		for i, item := range feed.Items {
			if item.GUID == *lastEntry {
				feed.Items = feed.Items[:i]
				break
			}
		}
	}

	return &FeedCheckResult{
		Feed:         feed,
		ETag:         newETag,
		LastModified: newLastModified,
	}, nil
}
