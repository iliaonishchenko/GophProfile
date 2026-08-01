package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/iliaonishchenko/GophProfile"
	"github.com/iliaonishchenko/GophProfile/internal/config"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		slog.Error("миграции остановлены", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", cfg.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("не удалось подключиться к PostgreSQL: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("не удалось проверить подключение к PostgreSQL: %w", err)
	}
	if err := gophprofile.RunMigrations(db); err != nil {
		return fmt.Errorf("не удалось применить миграции: %w", err)
	}
	slog.Info("миграции успешно применены")
	return nil
}
