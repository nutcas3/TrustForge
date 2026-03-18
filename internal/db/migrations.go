package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// Migrate runs schema migrations. In production use golang-migrate/migrate.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *logrus.Logger) error {
	_, err := pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("running schema migration: %w", err)
	}
	logger.Info("database schema migrated")
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS submissions (
    id               TEXT        PRIMARY KEY,
    contributor_id   TEXT        NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'PENDING',
    verifier_code    TEXT        NOT NULL,
    model_output     TEXT        NOT NULL DEFAULT '',
    score            DOUBLE PRECISION,
    red_team_report  JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_submissions_contributor
    ON submissions (contributor_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_submissions_status
    ON submissions (status, created_at ASC);

CREATE INDEX IF NOT EXISTS idx_submissions_score
    ON submissions (score DESC)
    WHERE status = 'TRUSTED';
`
