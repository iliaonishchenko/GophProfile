package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/iliaonishchenko/GophProfile"
	"github.com/iliaonishchenko/GophProfile/internal/broker"
	"github.com/iliaonishchenko/GophProfile/internal/config"
	"github.com/iliaonishchenko/GophProfile/internal/repository"
	"github.com/iliaonishchenko/GophProfile/internal/storage"
	"github.com/iliaonishchenko/GophProfile/internal/worker"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		slog.Error("воркер остановлен", "error", err)
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
		return fmt.Errorf("не удалось подключиться PostgreSQL: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("не удалось проверить подключение к PostgreSQL: %w", err)
	}
	if err := gophprofile.RunMigrations(db); err != nil {
		return err
	}

	objectStorage, err := storage.NewMinIO(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseSSL)
	if err != nil {
		return err
	}
	if err := objectStorage.EnsureBucket(context.Background()); err != nil {
		return err
	}
	rabbit, err := broker.NewRabbitMQ(cfg.AMQPURL, cfg.AMQPExchange, cfg.AMQPQueue)
	if err != nil {
		return err
	}
	defer func() { _ = rabbit.Close() }()

	processor := worker.New(repository.NewPostgres(db), objectStorage)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("воркер обработки аватарок запущен", "queue", cfg.AMQPQueue)
	return rabbit.Consume(ctx, processor.Handle)
}
