package storage

import (
	"cmp"
	"embed"
	"fmt"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	migrate "github.com/rubenv/sql-migrate"
)

//go:embed migrations/*
var embeddedMigrations embed.FS

type DB struct {
	*sqlx.DB
	Abonnements  AbonnementStorage
	Replacements ReplacementsStorage
}

func Connect() (*DB, error) {
	host := strings.TrimSpace(os.Getenv("MYSQL_HOST"))
	port := strings.TrimSpace(os.Getenv("MYSQL_PORT"))
	user := strings.TrimSpace(os.Getenv("MYSQL_USER"))
	password := strings.TrimSpace(os.Getenv("MYSQL_PASSWORD"))
	dbname := strings.TrimSpace(os.Getenv("MYSQL_DB"))
	tls := cmp.Or(strings.TrimSpace(os.Getenv("MYSQL_TLS")), "false")
	socket := strings.TrimSpace(os.Getenv("MYSQL_SOCKET"))

	var connectionString string
	if socket != "" {
		connectionString = fmt.Sprintf(
			"%s@unix(%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			user,
			socket,
			dbname,
		)
	} else {
		connectionString = fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local&tls=%s",
			user,
			password,
			host,
			port,
			dbname,
			tls,
		)
	}

	conn, err := sqlx.Connect("mysql", connectionString)
	if err != nil {
		return nil, err
	}

	conn.SetMaxIdleConns(100)
	conn.SetMaxOpenConns(100)

	return &DB{
		DB:           conn,
		Abonnements:  &Abonnements{DB: conn},
		Replacements: &Replacements{DB: conn},
	}, nil
}

func (db *DB) Migrate() (int, error) {
	migrations := &migrate.EmbedFileSystemMigrationSource{FileSystem: embeddedMigrations, Root: "migrations"}
	return migrate.Exec(db.DB.DB, "mysql", migrations, migrate.Up)
}
