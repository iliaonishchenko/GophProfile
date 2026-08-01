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

	"github.com/iliaonishchenko/GophProfile/internal/broker"
	"github.com/iliaonishchenko/GophProfile/internal/config"
	"github.com/iliaonishchenko/GophProfile/internal/observability"
	"github.com/iliaonishchenko/GophProfile/internal/repository"
	"github.com/iliaonishchenko/GophProfile/internal/storage"
	"github.com/iliaonishchenko/GophProfile/internal/worker"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/sync/errgroup"
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
		return fmt.Errorf("не удалось подключиться PostgreSQL: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("не удалось проверить подключение к PostgreSQL: %w", err)
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

	metrics := observability.NewMetrics()
	processor := worker.New(
		observability.TraceRepository(repository.NewPostgres(db)),
		observability.TraceStorage(objectStorage),
		metrics,
	)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metrics.Handler())
	metricsMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})
	metricsServer := &http.Server{
		Addr:              cfg.MetricsAddress,
		Handler:           metricsMux,
		ReadHeaderTimeout: cfg.ShutdownPeriod,
	}
	slog.Info("сервер метрик worker-а запущен", "address", cfg.MetricsAddress)
	slog.Info("воркер обработки аватарок запущен", "queue", cfg.AMQPQueue)
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		err := metricsServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("сервер метрик worker-а остановлен: %w", err)
	})
	group.Go(func() error {
		return rabbit.Consume(groupCtx, processor.Handle)
	})
	group.Go(func() error {
		<-groupCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownPeriod)
		defer cancel()
		return metricsServer.Shutdown(shutdownCtx)
	})
	return group.Wait()
}
