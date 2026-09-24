package storage

import (
	"errors"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	migrate "github.com/rubenv/sql-migrate"
)

// Integration tests run against a real MySQL/MariaDB when RSSBOT_TEST_DSN is
// set, e.g. "user:pass@tcp(127.0.0.1:3306)/rssbot_test?parseTime=True&loc=Local".
// The database is wiped before every test.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("RSSBOT_TEST_DSN")
	if dsn == "" {
		t.Skip("RSSBOT_TEST_DSN not set")
	}

	conn, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	for _, table := range []string{"abonnements", "chats", "feeds", "replacements", "gorp_migrations"} {
		if _, err := conn.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			t.Fatal(err)
		}
	}

	db := &DB{DB: conn, Abonnements: &Abonnements{DB: conn}, Replacements: &Replacements{DB: conn}}
	if _, err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustCreate(t *testing.T, db *DB, chatID int64, url string, nextPollAt time.Time) {
	t.Helper()
	if err := db.Abonnements.Create(chatID, "chat", url, nil, nil, nil, PollHints{}, nextPollAt); err != nil {
		t.Fatal(err)
	}
}

func feedByURL(t *testing.T, db *DB, url string) (Abonnement, bool) {
	t.Helper()
	all, err := db.Abonnements.GetAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, ab := range all {
		if ab.Url == url {
			return ab, true
		}
	}
	return Abonnement{}, false
}

func dueURLs(t *testing.T, db *DB) []string {
	t.Helper()
	due, err := db.Abonnements.GetDue()
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, ab := range due {
		urls = append(urls, ab.Url)
	}
	sort.Strings(urls)
	return urls
}

func chatIDs(ab Abonnement) []int64 {
	var ids []int64
	for _, c := range ab.Chats {
		ids = append(ids, c.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func TestMigrationsRoundTrip(t *testing.T) {
	db := newTestDB(t)
	source := &migrate.EmbedFileSystemMigrationSource{FileSystem: embeddedMigrations, Root: "migrations"}

	down, err := migrate.Exec(db.DB.DB, "mysql", source, migrate.Down)
	if err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	up, err := migrate.Exec(db.DB.DB, "mysql", source, migrate.Up)
	if err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if down != up || up == 0 {
		t.Errorf("down=%d up=%d, want equal and non-zero", down, up)
	}
}

func TestGetDueRespectsSchedule(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	mustCreate(t, db, 1, "https://example.org/past", now.Add(-time.Minute))
	mustCreate(t, db, 1, "https://example.org/soon", now.Add(time.Hour))
	mustCreate(t, db, 1, "https://example.org/later", now.Add(10*time.Hour))

	got := dueURLs(t, db)
	if len(got) != 1 || got[0] != "https://example.org/past" {
		t.Errorf("due feeds = %v, want only the past one (time zone mismatch?)", got)
	}
}

func TestCreateSharesFeedBetweenChats(t *testing.T) {
	db := newTestDB(t)
	url := "https://example.org/feed"
	mustCreate(t, db, 1, url, time.Now())
	mustCreate(t, db, 2, url, time.Now())

	all, err := db.Abonnements.GetAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d feeds, want 1", len(all))
	}
	if ids := chatIDs(all[0]); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Errorf("chats = %v, want [1 2]", ids)
	}

	feeds, err := db.Abonnements.GetByUser(2)
	if err != nil {
		t.Fatalf("GetByUser must map every feeds column: %v", err)
	}
	if len(feeds) != 1 || feeds[0].Url != url {
		t.Errorf("GetByUser = %+v", feeds)
	}
}

func TestExists(t *testing.T) {
	db := newTestDB(t)
	url := "https://example.org/feed"
	mustCreate(t, db, 1, url, time.Now())
	ab, _ := feedByURL(t, db, url)

	checks := []struct {
		name string
		fn   func() (bool, error)
		want bool
	}{
		{"by url", func() (bool, error) { return db.Abonnements.ExistsByFeedUrl(1, url) }, true},
		{"by url other chat", func() (bool, error) { return db.Abonnements.ExistsByFeedUrl(2, url) }, false},
		{"by url unknown", func() (bool, error) { return db.Abonnements.ExistsByFeedUrl(1, "https://nope") }, false},
		{"by id", func() (bool, error) { return db.Abonnements.ExistsById(1, ab.ID) }, true},
		{"by id unknown", func() (bool, error) { return db.Abonnements.ExistsById(1, ab.ID+1) }, false},
	}
	for _, c := range checks {
		got, err := c.fn()
		if err != nil || got != c.want {
			t.Errorf("%s = %v, %v; want %v, nil", c.name, got, err, c.want)
		}
	}
}

func TestSetFeedStatePersistsCacheAndHints(t *testing.T) {
	db := newTestDB(t)
	url := "https://example.org/feed"
	mustCreate(t, db, 1, url, time.Now())
	ab, _ := feedByURL(t, db, url)

	if err := db.Abonnements.Reschedule(ab.ID, time.Now(), 3, 0); err != nil {
		t.Fatal(err)
	}

	entry, etag, lm := "guid-1", `"v1"`, "Wed, 21 Oct 2015 07:28:00 GMT"
	hints := PollHints{Interval: 2 * time.Hour, SkipHours: []int{1, 2}, SkipDays: []string{"Sunday"}}
	next := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := db.Abonnements.SetFeedState(ab.ID, &entry, &etag, &lm, hints, next, 0, 4); err != nil {
		t.Fatal(err)
	}

	got, _ := feedByURL(t, db, url)
	if got.LastEntry.String != entry || got.ETag.String != etag || got.LastModified.String != lm {
		t.Errorf("cache state not persisted: %+v", got.Feed)
	}
	if got.ErrorCount != 0 || got.UnchangedCount != 4 || got.FailingSince.Valid {
		t.Errorf("counters not reset: errors=%d unchanged=%d failingSince=%v", got.ErrorCount, got.UnchangedCount, got.FailingSince)
	}
	if !got.NextPollAt.Time.Equal(next) {
		t.Errorf("next_poll_at = %s, want %s", got.NextPollAt.Time, next)
	}
	if h := got.Hints(); h.Interval != hints.Interval || len(h.SkipHours) != 2 || len(h.SkipDays) != 1 {
		t.Errorf("hints = %+v, want %+v", h, hints)
	}
}

func TestRescheduleTracksFailingSince(t *testing.T) {
	db := newTestDB(t)
	url := "https://example.org/feed"
	mustCreate(t, db, 1, url, time.Now())
	ab, _ := feedByURL(t, db, url)

	if err := db.Abonnements.Reschedule(ab.ID, time.Now(), 1, 0); err != nil {
		t.Fatal(err)
	}
	first, _ := feedByURL(t, db, url)
	if !first.FailingSince.Valid {
		t.Fatal("failing_since not set on first error")
	}

	time.Sleep(1100 * time.Millisecond)
	if err := db.Abonnements.Reschedule(ab.ID, time.Now(), 2, 0); err != nil {
		t.Fatal(err)
	}
	second, _ := feedByURL(t, db, url)
	if !second.FailingSince.Time.Equal(first.FailingSince.Time) {
		t.Errorf("failing_since moved from %s to %s", first.FailingSince.Time, second.FailingSince.Time)
	}

	if err := db.Abonnements.Reschedule(ab.ID, time.Now(), 0, 1); err != nil {
		t.Fatal(err)
	}
	recovered, _ := feedByURL(t, db, url)
	if recovered.FailingSince.Valid || recovered.ErrorCount != 0 {
		t.Errorf("error streak not cleared: %+v", recovered.Feed)
	}
}

func TestDisableAndReactivate(t *testing.T) {
	db := newTestDB(t)
	url := "https://example.org/feed"
	mustCreate(t, db, 1, url, time.Now().Add(-time.Minute))
	ab, _ := feedByURL(t, db, url)

	if err := db.Abonnements.Reschedule(ab.ID, time.Now().Add(-time.Minute), 5, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Abonnements.DisableFeed(ab.ID, "HTTP 410 Gone"); err != nil {
		t.Fatal(err)
	}
	if due := dueURLs(t, db); len(due) != 0 {
		t.Errorf("disabled feed is due: %v", due)
	}
	disabled, _ := feedByURL(t, db, url)
	if !disabled.Disabled || disabled.DisabledReason.String != "HTTP 410 Gone" {
		t.Errorf("feed not disabled: %+v", disabled.Feed)
	}

	ok, err := db.Abonnements.ReactivateFeed(url, time.Now().Add(-time.Second))
	if err != nil || !ok {
		t.Fatalf("ReactivateFeed = %v, %v; want true", ok, err)
	}
	reactivated, _ := feedByURL(t, db, url)
	if reactivated.Disabled || reactivated.DisabledReason.Valid || reactivated.ErrorCount != 0 || reactivated.FailingSince.Valid {
		t.Errorf("feed not fully reactivated: %+v", reactivated.Feed)
	}
	if due := dueURLs(t, db); len(due) != 1 {
		t.Errorf("reactivated feed is not due")
	}

	if ok, err := db.Abonnements.ReactivateFeed(url, time.Now()); err != nil || ok {
		t.Errorf("ReactivateFeed on active feed = %v, %v; want false", ok, err)
	}
}

func TestMoveFeedURLRenames(t *testing.T) {
	db := newTestDB(t)
	mustCreate(t, db, 1, "http://example.org/feed", time.Now())
	ab, _ := feedByURL(t, db, "http://example.org/feed")

	merged, err := db.Abonnements.MoveFeedURL(ab.ID, "https://example.org/feed")
	if err != nil || merged {
		t.Fatalf("MoveFeedURL = %v, %v; want false, nil", merged, err)
	}
	if _, ok := feedByURL(t, db, "https://example.org/feed"); !ok {
		t.Error("feed not renamed")
	}
}

func TestMoveFeedURLMergesIntoExistingFeed(t *testing.T) {
	db := newTestDB(t)
	oldURL, newURL := "http://example.org/old", "https://example.org/new"
	later := time.Now().Add(time.Hour)
	mustCreate(t, db, 1, oldURL, later)
	mustCreate(t, db, 2, oldURL, later)
	mustCreate(t, db, 2, newURL, later)
	mustCreate(t, db, 3, newURL, later)
	old, _ := feedByURL(t, db, oldURL)

	merged, err := db.Abonnements.MoveFeedURL(old.ID, newURL)
	if err != nil || !merged {
		t.Fatalf("MoveFeedURL = %v, %v; want true, nil", merged, err)
	}
	if _, ok := feedByURL(t, db, oldURL); ok {
		t.Error("old feed still exists")
	}
	target, _ := feedByURL(t, db, newURL)
	if ids := chatIDs(target); len(ids) != 3 {
		t.Errorf("chats after merge = %v, want [1 2 3]", ids)
	}
	if due := dueURLs(t, db); len(due) != 1 || due[0] != newURL {
		t.Errorf("merged feed should be due immediately, due = %v", due)
	}
}

func TestDeleteRemovesOrphans(t *testing.T) {
	db := newTestDB(t)
	shared, single := "https://example.org/shared", "https://example.org/single"
	mustCreate(t, db, 1, shared, time.Now())
	mustCreate(t, db, 2, shared, time.Now())
	mustCreate(t, db, 1, single, time.Now())
	sharedFeed, _ := feedByURL(t, db, shared)
	singleFeed, _ := feedByURL(t, db, single)

	if err := db.Abonnements.Delete(1, singleFeed.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := feedByURL(t, db, single); ok {
		t.Error("orphaned feed not deleted")
	}

	if err := db.Abonnements.Delete(2, sharedFeed.ID); err != nil {
		t.Fatal(err)
	}
	remaining, ok := feedByURL(t, db, shared)
	if !ok || len(remaining.Chats) != 1 || remaining.Chats[0].ID != 1 {
		t.Errorf("shared feed should remain for chat 1: %+v", remaining)
	}

	var chats int
	if err := db.Get(&chats, "SELECT COUNT(*) FROM chats"); err != nil {
		t.Fatal(err)
	}
	if chats != 1 {
		t.Errorf("got %d chats, want 1 (chat 2 has no abonnements left)", chats)
	}
}

func TestReplacements(t *testing.T) {
	db := newTestDB(t)

	seeded, err := db.Replacements.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(seeded) == 0 {
		t.Fatal("default replacements missing")
	}

	if err := db.Replacements.Create("[werbung]", false); err != nil {
		t.Fatal(err)
	}
	err = db.Replacements.Create("[werbung]", false)
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1062 {
		t.Errorf("duplicate replacement: err = %v, want MySQL error 1062", err)
	}

	list, err := db.Replacements.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(seeded)+1 {
		t.Fatalf("got %d replacements, want %d", len(list), len(seeded)+1)
	}

	var id int64
	for _, r := range list {
		if r.Value == "[werbung]" {
			id = r.ID
		}
	}
	if err := db.Replacements.Delete(id); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if err := db.Replacements.Delete(id); err == nil {
		t.Error("deleting a missing replacement should fail")
	}
}
