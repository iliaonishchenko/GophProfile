package service

import (
	"context"
	"database/sql"
)

type Checker interface {
	Health(context.Context) error
}

type HealthResult struct {
	Database error
	S3       error
	Broker   error
}

type HealthService struct {
	database *sql.DB
	storage  Checker
	broker   Checker
}

func NewHealthService(database *sql.DB, storage, broker Checker) *HealthService {
	return &HealthService{database: database, storage: storage, broker: broker}
}

func (s *HealthService) Check(ctx context.Context) HealthResult {
	return HealthResult{
		Database: s.database.PingContext(ctx),
		S3:       s.storage.Health(ctx),
		Broker:   s.broker.Health(ctx),
	}
}
