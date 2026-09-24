package storage

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestPollHintsRoundTrip(t *testing.T) {
	in := PollHints{Interval: 90 * time.Minute, SkipHours: []int{0, 23}, SkipDays: []string{"Saturday", "Sunday"}}
	interval, skipHours, skipDays := in.encode()

	feed := Feed{FeedInterval: interval}
	if skipHours != nil {
		feed.SkipHours = sql.NullString{String: *skipHours, Valid: true}
	}
	if skipDays != nil {
		feed.SkipDays = sql.NullString{String: *skipDays, Valid: true}
	}

	if out := feed.Hints(); !reflect.DeepEqual(in, out) {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
}

func TestPollHintsEmpty(t *testing.T) {
	interval, skipHours, skipDays := PollHints{}.encode()
	if interval != 0 || skipHours != nil || skipDays != nil {
		t.Errorf("empty hints encoded to %d %v %v", interval, skipHours, skipDays)
	}
	if h := (Feed{}).Hints(); h.Interval != 0 || h.SkipHours != nil || h.SkipDays != nil {
		t.Errorf("empty feed decoded to %+v", h)
	}
}
