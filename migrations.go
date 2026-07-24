package gophprofile

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//
//go:embed migrations
var embeddedMigrations embed.FS

func RunMigrations(db *sql.DB) error {
	goose.SetBaseFS(embeddedMigrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("не удалось настроить диалект миграций PostgreSQL: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("не удалось применить миграции PostgreSQL: %w", err)
	}

	return nil
}
