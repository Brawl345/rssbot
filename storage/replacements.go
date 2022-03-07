package storage

import (
	"errors"
	"github.com/jmoiron/sqlx"
	"time"
)

type (
	ReplacementsStorage interface {
		Create(value string, isRegex bool) error
		Delete(replacementId int64) error
		List() ([]Replacement, error)
	}

	Replacements struct {
		*sqlx.DB
	}

	Replacement struct {
		ID        int64     `db:"id"`
		Value     string    `db:"value"`
		IsRegex   bool      `db:"is_regex"`
		CreatedAt time.Time `db:"created_at"`
	}
)

func (db *Replacements) Create(value string, isRegex bool) error {
	const query = `INSERT INTO replacements (value, is_regex) VALUES (?, ?)`
	_, err := db.Exec(query, value, isRegex)
	return err
}

func (db *Replacements) List() ([]Replacement, error) {
	const query = `SELECT * FROM replacements`

	var replacements []Replacement
	err := db.Select(&replacements, query)
	return replacements, err
}

func (db *Replacements) Delete(replacementId int64) error {
	const query = `DELETE FROM replacements WHERE id = ?`
	res, err := db.Exec(query, replacementId)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if rows == 0 {
		return errors.New("replacement not found")
	}
	return err
}
