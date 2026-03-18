package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nutcas3/trustforge/pkg/models"
)

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

// ListByStatus returns all submissions in a given status
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
