// Package scoring evaluates EvaluationResults and produces final submission scores
package scoring

import (
	"context"
	"fmt"
	"time"

	"github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

// Scorer applies scoring policy to raw evaluation results
type Scorer struct {
	logger *logrus.Logger
}

// NewScorer creates a new Scorer
func NewScorer(logger *logrus.Logger) *Scorer {
	return &Scorer{logger: logger}
}

// ScoreSubmission takes the raw evaluation result and red-team report
// and produces a final trust decision + score for the submission.
func (s *Scorer) ScoreSubmission(
	ctx context.Context,
	submission *models.Submission,
	evalResult *models.EvaluationResult,
	redTeamReport *models.RedTeamReport,
) error {
	logger := s.logger.WithField("submission_id", submission.ID)

	if evalResult.ExitCode != 0 {
		submission.Status = models.StatusRejected
		logger.WithField("exit_code", evalResult.ExitCode).Warn("rejecting: non-zero exit code")
		return s.finalize(submission, 0.0, redTeamReport)
	}

	// --- Gate 2: Must not have timed out ---
	if evalResult.TimedOut {
		submission.Status = models.StatusRejected
		logger.Warn("rejecting: execution timed out")
		return s.finalize(submission, 0.0, redTeamReport)
	}

	// --- Gate 3: Red-team must pass ---
	if redTeamReport != nil && !redTeamReport.PassedRedTeam {
		submission.Status = models.StatusRejected
		logger.WithField("risk_score", redTeamReport.RiskScore).
			Warn("rejecting: failed red-team analysis")
		return s.finalize(submission, 0.0, redTeamReport)
	}

	// --- Gate 4: Score must be in valid range ---
	if evalResult.Score < 0.0 || evalResult.Score > 1.0 {
		submission.Status = models.StatusRejected
		logger.WithField("score", evalResult.Score).
			Warn("rejecting: score out of valid range [0.0, 1.0]")
		return s.finalize(submission, 0.0, redTeamReport)
	}

	// --- All gates passed: TRUSTED ---
	submission.Status = models.StatusTrusted
	logger.WithField("score", evalResult.Score).Info("submission TRUSTED")

	return s.finalize(submission, evalResult.Score, redTeamReport)
}

func (s *Scorer) finalize(submission *models.Submission, score float64, report *models.RedTeamReport) error {
	now := time.Now()
	submission.Score = &score
	submission.RedTeamReport = report
	submission.CompletedAt = &now
	submission.UpdatedAt = now

	if submission.Status != models.StatusTrusted && submission.Status != models.StatusRejected {
		return fmt.Errorf("scoring left submission in invalid state: %s", submission.Status)
	}
	return nil
}
