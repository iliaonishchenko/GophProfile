package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddress    string
	DatabaseDSN    string
	AMQPURL        string
	AMQPExchange   string
	AMQPQueue      string
	S3Endpoint     string
	S3AccessKey    string
	S3SecretKey    string
	S3Bucket       string
	S3UseSSL       bool
	PublicBaseURL  string
	ShutdownPeriod time.Duration
}

func Load() (Config, error) {
	useSSL, err := strconv.ParseBool(value("S3_USE_SSL", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("не удалось разобрать значение S3_USE_SSL: %w", err)
	}

	shutdownPeriod, err := time.ParseDuration(value("SHUTDOWN_PERIOD", "10s"))
	if err != nil {
		return Config{}, fmt.Errorf("не удалось разобрать значение SHUTDOWN_PERIOD: %w", err)
	}

	return Config{
		HTTPAddress:    value("HTTP_ADDRESS", ":8080"),
		DatabaseDSN:    value("DATABASE_DSN", "postgres://gophprofile:gophprofile@localhost:5432/gophprofile?sslmode=disable"),
		AMQPURL:        value("AMQP_URL", "amqp://gophprofile:gophprofile@localhost:5672/"),
		AMQPExchange:   value("AMQP_EXCHANGE", "avatars.exchange"),
		AMQPQueue:      value("AMQP_QUEUE", "avatars.worker"),
		S3Endpoint:     value("S3_ENDPOINT", "localhost:9000"),
		S3AccessKey:    value("S3_ACCESS_KEY", "gophprofile"),
		S3SecretKey:    value("S3_SECRET_KEY", "gophprofile-secret"),
		S3Bucket:       value("S3_BUCKET", "avatars"),
		S3UseSSL:       useSSL,
		PublicBaseURL:  os.Getenv("PUBLIC_BASE_URL"),
		ShutdownPeriod: shutdownPeriod,
	}, nil
}

func value(name, fallback string) string {
	if current := os.Getenv(name); current != "" {
		return current
	}
	return fallback
}
