package unit_test

import (
	"context"
	"testing"
	"time"

	"github.com/nutcas3/trustforge/internal/scoring"
	"github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

func newLogger() *logrus.Logger {
	l := logrus.New()
	l.SetLevel(logrus.WarnLevel) // quiet in tests
	return l
}

func TestScorer_TrustedWhenAllGatesPass(t *testing.T) {
	scorer := scoring.NewScorer(newLogger())

	score := 0.85
	evalResult := &models.EvaluationResult{
		SubmissionID: "test-001",
		ExitCode:     0,
		Score:        score,
		ElapsedMs:    500,
		TimedOut:     false,
	}
	redTeam := &models.RedTeamReport{
		RiskScore:     0.1,
		PassedRedTeam: true,
	}
	sub := &models.Submission{
		ID:        "test-001",
		Status:    models.StatusRunning,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := scorer.ScoreSubmission(context.Background(), sub, evalResult, redTeam); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub.Status != models.StatusTrusted {
		t.Errorf("expected TRUSTED, got %s", sub.Status)
	}
	if sub.Score == nil || *sub.Score != score {
		t.Errorf("expected score %.2f, got %v", score, sub.Score)
	}
	if sub.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
}

func TestScorer_RejectedOnNonZeroExit(t *testing.T) {
	scorer := scoring.NewScorer(newLogger())

	evalResult := &models.EvaluationResult{
		ExitCode: 1,
		Stderr:   "syntax error",
	}
	sub := &models.Submission{ID: "test-002", Status: models.StatusRunning}

	scorer.ScoreSubmission(context.Background(), sub, evalResult, nil)

	if sub.Status != models.StatusRejected {
		t.Errorf("expected REJECTED on exit code 1, got %s", sub.Status)
	}
}

func TestScorer_RejectedOnTimeout(t *testing.T) {
	scorer := scoring.NewScorer(newLogger())

	evalResult := &models.EvaluationResult{
		ExitCode: -1,
		TimedOut: true,
	}
	sub := &models.Submission{ID: "test-003", Status: models.StatusRunning}

	scorer.ScoreSubmission(context.Background(), sub, evalResult, nil)

	if sub.Status != models.StatusRejected {
		t.Errorf("expected REJECTED on timeout, got %s", sub.Status)
	}
}

func TestScorer_RejectedWhenRedTeamFails(t *testing.T) {
	scorer := scoring.NewScorer(newLogger())

	evalResult := &models.EvaluationResult{ExitCode: 0, Score: 1.0}
	redTeam := &models.RedTeamReport{
		RiskScore:     0.95,
		PassedRedTeam: false,
	}
	sub := &models.Submission{ID: "test-004", Status: models.StatusRunning}

	scorer.ScoreSubmission(context.Background(), sub, evalResult, redTeam)

	if sub.Status != models.StatusRejected {
		t.Errorf("expected REJECTED when red team fails, got %s", sub.Status)
	}
}

func TestScorer_RejectedOnInvalidScore(t *testing.T) {
	scorer := scoring.NewScorer(newLogger())

	cases := []float64{-0.1, 1.1, 2.0, -1.0}
	for _, sc := range cases {
		sub := &models.Submission{ID: "test-score", Status: models.StatusRunning}
		evalResult := &models.EvaluationResult{ExitCode: 0, Score: sc}
		redTeam := &models.RedTeamReport{PassedRedTeam: true, RiskScore: 0.1}

		scorer.ScoreSubmission(context.Background(), sub, evalResult, redTeam)

		if sub.Status != models.StatusRejected {
			t.Errorf("score=%.2f: expected REJECTED, got %s", sc, sub.Status)
		}
	}
}

func TestSubmissionStatusTransitions(t *testing.T) {
	// Valid pipeline: PENDING -> SANDBOXING -> RUNNING -> RED_TEAM -> TRUSTED
	pipeline := []models.SubmissionStatus{
		models.StatusPending,
		models.StatusSandboxing,
		models.StatusRunning,
		models.StatusRedTeam,
		models.StatusTrusted,
	}

	sub := &models.Submission{ID: "test-pipeline"}
	for _, status := range pipeline {
		sub.Status = status
	}
}

func TestRedTeamReport_PassThreshold(t *testing.T) {
	cases := []struct {
		riskScore float64
		threshold float64
		wantPass  bool
	}{
		{0.5, 0.7, true},
		{0.7, 0.7, false}, // exactly at threshold = fail
		{0.69, 0.7, true},
		{0.95, 0.7, false},
		{0.0, 0.7, true},
		{1.0, 0.7, false},
	}

	for _, tc := range cases {
		passed := tc.riskScore < tc.threshold
		if passed != tc.wantPass {
			t.Errorf("riskScore=%.2f threshold=%.2f: expected pass=%v, got %v",
				tc.riskScore, tc.threshold, tc.wantPass, passed)
		}
	}
}
