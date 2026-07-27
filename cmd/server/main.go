package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/iliaonishchenko/GophProfile"
	"github.com/iliaonishchenko/GophProfile/internal/api"
	"github.com/iliaonishchenko/GophProfile/internal/broker"
	"github.com/iliaonishchenko/GophProfile/internal/config"
	"github.com/iliaonishchenko/GophProfile/internal/httpserver"
	"github.com/iliaonishchenko/GophProfile/internal/observability"
	"github.com/iliaonishchenko/GophProfile/internal/repository"
	"github.com/iliaonishchenko/GophProfile/internal/service"
	"github.com/iliaonishchenko/GophProfile/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		slog.Error("сервер остановлен", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	shutdownTelemetry, err := observability.Setup(context.Background(), cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		return fmt.Errorf("не удалось настроить observability: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownPeriod)
		defer cancel()
		if err := shutdownTelemetry(ctx); err != nil {
			slog.Error("не удалось остановить экспорт телеметрии", "error", err)
		}
	}()
	db, err := sql.Open("pgx", cfg.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("не удалось подключиться к PostgreSQL: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("не удалось подключиться к PostgreSQL: %w", err)
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

	repo := observability.TraceRepository(repository.NewPostgres(db))
	tracedStorage := observability.TraceStorage(objectStorage)
	tracedPublisher := observability.TracePublisher(rabbit)
	metrics := observability.NewMetrics()
	avatarService := service.NewAvatarService(repo, tracedStorage, tracedPublisher, cfg.PublicBaseURL)
	healthService := service.NewHealthService(db, tracedStorage, tracedPublisher)
	handler := httpserver.NewHandler(avatarService, healthService, metrics)

	router := chi.NewRouter()
	router.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	router.Use(metrics.HTTPMiddleware)
	router.Handle("/metrics", metrics.Handler())
	api.HandlerFromMux(handler, router)
	router.Handle("/*", http.FileServer(http.Dir("web/static")))

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           observability.TraceHTTPHandler(router, cfg.ServiceName),
		ReadHeaderTimeout: cfg.ShutdownPeriod,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		slog.Info("HTTP-сервер запущен", "address", cfg.HTTPAddress)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownPeriod)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
