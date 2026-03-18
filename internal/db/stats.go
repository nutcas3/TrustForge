package db

import (
	"context"
	"fmt"
)

type Stats struct {
	Total    int64            `json:"total"`
	ByStatus map[string]int64 `json:"by_status"`
	AvgScore float64          `json:"avg_score"`
	P95Score float64          `json:"p95_score"`
}

func (r *Repository) Stats(ctx context.Context) (*Stats, error) {
	stats := &Stats{ByStatus: make(map[string]int64)}

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
