package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

// Repository manages all database operations for submissions
type Repository struct {
	pool   *pgxpool.Pool
	logger *logrus.Logger
}

// NewRepository creates a new Repository and validates connectivity
func NewRepository(ctx context.Context, cfg config.DatabaseConfig, logger *logrus.Logger) (*Repository, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parsing DSN: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.MaxConns)
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	logger.Info("database connected")
	return &Repository{pool: pool, logger: logger}, nil
}

// Close releases all pool connections
func (r *Repository) Close() {
	r.pool.Close()
}

// Migrate runs schema migrations. In production use golang-migrate/migrate.
func (r *Repository) Migrate(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("running schema migration: %w", err)
	}
	r.logger.Info("database schema migrated")
	return nil
}

// Create inserts a new submission record
func (r *Repository) Create(ctx context.Context, sub *models.Submission) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO submissions (
			id, contributor_id, status, verifier_code, model_output,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		sub.ID,
		sub.ContributorID,
		string(sub.Status),
		sub.VerifierCode,
		sub.ModelOutput,
		sub.CreatedAt,
		sub.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting submission %s: %w", sub.ID, err)
	}
	return nil
}

// GetByID fetches a submission by its ID
func (r *Repository) GetByID(ctx context.Context, id string) (*models.Submission, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, contributor_id, status, verifier_code, model_output,
		       score, red_team_report, created_at, updated_at, completed_at
		FROM submissions WHERE id = $1`, id)

	return scanSubmission(row)
}

// UpdateStatus atomically updates the status of a submission
func (r *Repository) UpdateStatus(ctx context.Context, id string, status models.SubmissionStatus) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE submissions SET status = $1, updated_at = NOW()
		WHERE id = $2`,
		string(status), id,
	)
	if err != nil {
		return fmt.Errorf("updating status for %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("submission %s not found", id)
	}
	return nil
}

// Complete writes the final score, red-team report, and marks status as complete.
// This is called once after the full pipeline finishes.
func (r *Repository) Complete(ctx context.Context, sub *models.Submission) error {
	var reportJSON []byte
	var err error
	if sub.RedTeamReport != nil {
		reportJSON, err = json.Marshal(sub.RedTeamReport)
		if err != nil {
			return fmt.Errorf("marshaling red team report: %w", err)
		}
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE submissions
		SET status        = $1,
		    score         = $2,
		    red_team_report = $3,
		    updated_at    = NOW(),
		    completed_at  = $4
		WHERE id = $5`,
		string(sub.Status),
		sub.Score,
		reportJSON,
		sub.CompletedAt,
		sub.ID,
	)
	if err != nil {
		return fmt.Errorf("completing submission %s: %w", sub.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("submission %s not found", sub.ID)
	}
	return nil
}

// ListByContributor returns paginated submissions for a given contributor.
// Results are ordered newest-first.
func (r *Repository) ListByContributor(ctx context.Context, contributorID string, limit, offset int) ([]*models.Submission, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, contributor_id, status, verifier_code, model_output,
		       score, red_team_report, created_at, updated_at, completed_at
		FROM submissions
		WHERE contributor_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`,
		contributorID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("listing submissions: %w", err)
	}
	defer rows.Close()

	return collectSubmissions(rows)
}

// ListByStatus returns all submissions in a given status (for retries/monitoring)
func (r *Repository) ListByStatus(ctx context.Context, status models.SubmissionStatus, limit int) ([]*models.Submission, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, contributor_id, status, verifier_code, model_output,
		       score, red_team_report, created_at, updated_at, completed_at
		FROM submissions
		WHERE status = $1
		ORDER BY created_at ASC
		LIMIT $2`,
		string(status), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing submissions by status: %w", err)
	}
	defer rows.Close()

	return collectSubmissions(rows)
}

// Stats returns aggregate metrics for the dashboard
type Stats struct {
	Total    int64            `json:"total"`
	ByStatus map[string]int64 `json:"by_status"`
	AvgScore float64          `json:"avg_score"`
	P95Score float64          `json:"p95_score"`
}

func (r *Repository) Stats(ctx context.Context) (*Stats, error) {
	stats := &Stats{ByStatus: make(map[string]int64)}

	// Total count per status
	rows, err := r.pool.Query(ctx, `
		SELECT status, COUNT(*) FROM submissions GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("querying stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		stats.ByStatus[status] = count
		stats.Total += count
	}

	// Score percentiles for trusted submissions
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(AVG(score), 0),
		       COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY score), 0)
		FROM submissions
		WHERE status = 'TRUSTED' AND score IS NOT NULL`,
	).Scan(&stats.AvgScore, &stats.P95Score)
	if err != nil {
		return nil, fmt.Errorf("querying score percentiles: %w", err)
	}

	return stats, nil
}

func scanSubmission(row pgx.Row) (*models.Submission, error) {
	var sub models.Submission
	var statusStr string
	var reportJSON []byte

	err := row.Scan(
		&sub.ID,
		&sub.ContributorID,
		&statusStr,
		&sub.VerifierCode,
		&sub.ModelOutput,
		&sub.Score,
		&reportJSON,
		&sub.CreatedAt,
		&sub.UpdatedAt,
		&sub.CompletedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning submission: %w", err)
	}

	sub.Status = models.SubmissionStatus(statusStr)

	if len(reportJSON) > 0 {
		sub.RedTeamReport = &models.RedTeamReport{}
		if err := json.Unmarshal(reportJSON, sub.RedTeamReport); err != nil {
			return nil, fmt.Errorf("unmarshaling red team report: %w", err)
		}
	}

	return &sub, nil
}

func collectSubmissions(rows pgx.Rows) ([]*models.Submission, error) {
	var subs []*models.Submission
	for rows.Next() {
		var sub models.Submission
		var statusStr string
		var reportJSON []byte

		err := rows.Scan(
			&sub.ID,
			&sub.ContributorID,
			&statusStr,
			&sub.VerifierCode,
			&sub.ModelOutput,
			&sub.Score,
			&reportJSON,
			&sub.CreatedAt,
			&sub.UpdatedAt,
			&sub.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		sub.Status = models.SubmissionStatus(statusStr)
		if len(reportJSON) > 0 {
			sub.RedTeamReport = &models.RedTeamReport{}
			json.Unmarshal(reportJSON, sub.RedTeamReport)
		}
		subs = append(subs, &sub)
	}
	return subs, rows.Err()
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
