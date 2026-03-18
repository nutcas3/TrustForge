package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/sirupsen/logrus"
)

// Repository manages all database operations for submissions
type Repository struct {
	pool   *pgxpool.Pool
	logger *logrus.Logger
}

// NewRepository creates a new Repository and validates connectivity
func NewRepository(ctx context.Context, cfg config.DatabaseConfig, logger *logrus.Logger) (*Repository, error) {
	pool, err := NewPool(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}

	return &Repository{pool: pool, logger: logger}, nil
}

// Close releases all pool connections
func (r *Repository) Close() {
	r.pool.Close()
}

// Migrate runs schema migrations
func (r *Repository) Migrate(ctx context.Context) error {
	return Migrate(ctx, r.pool, r.logger)
}
