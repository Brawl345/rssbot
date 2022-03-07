package storage

import (
	"database/sql"
	"github.com/mmcdole/gofeed"
	"time"
)

type Feed struct {
	ID        int64          `db:"id"`
	Url       string         `db:"url"`
	LastEntry sql.NullString `db:"last_entry"`
	CreatedAt time.Time      `db:"created_at"`
	UpdatedAt sql.NullTime   `db:"updated_at"`
}

func (feedToCheck Feed) Check(lastEntry *string) (*gofeed.Feed, error) {

	feed, err := gofeed.NewParser().ParseURL(feedToCheck.Url)
	if err != nil {
		return nil, err
	}

	if lastEntry != nil {
		for i, item := range feed.Items {
			if item.GUID == *lastEntry {
				feed.Items = feed.Items[:i]
				return feed, nil
			}
		}
	}

	return feed, nil
}
