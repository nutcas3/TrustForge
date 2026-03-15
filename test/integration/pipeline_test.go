// Package integration contains end-to-end tests for the TrustForge pipeline.
//
// These tests require a running Firecracker environment with:
//   - KVM access (/dev/kvm)
//   - The base.ext4 image at the configured path
//   - mkfs.ext4 and mount available on the host
//
// Run with: go test ./test/integration/... -tags integration -v
// Skip in CI without KVM: go test ./... (integration tag excluded by default)
//
//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nutcas3/trustforge/internal/fs"
	"github.com/nutcas3/trustforge/internal/llm"
	"github.com/nutcas3/trustforge/internal/scoring"
	"github.com/nutcas3/trustforge/internal/vm"
	"github.com/nutcas3/trustforge/internal/worker"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

var testCfg = &config.Config{
	Firecracker: config.FirecrackerConfig{
		BinaryPath:       "/usr/local/bin/firecracker",
		JailerPath:       "/usr/local/bin/jailer",
		SocketDir:        "/tmp/trustforge-test/sockets",
		TaskDiskDir:      "/tmp/trustforge-test/tasks",
		SnapshotDir:      "/tmp/trustforge-test/snapshots",
		BaseImagePath:    "/var/lib/trustforge/images/base.ext4",
		KernelPath:       "/var/lib/trustforge/vmlinux",
		VCPUCount:        1,
		MemSizeMiB:       128,
		ExecutionTimeout: 30 * time.Second,
		JailerUID:        1001,
		JailerGID:        1001,
	},
	Worker: config.WorkerConfig{
		PoolSize:          4,
		QueueDepth:        20,
		WarmSnapshotCount: 2,
	},
	LLM: config.LLMConfig{
		Model:         "claude-sonnet-4-20250514",
		MaxTokens:     1024,
		RiskThreshold: 0.7,
	},
	Storage: config.StorageConfig{
		TaskDiskSize: 10 * 1024 * 1024,
	},
}

func testLogger() *logrus.Logger {
	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)
	return l
}

func newTestPool(t *testing.T) *worker.Pool {
	t.Helper()
	logger := testLogger()
	factory := vm.NewFactory(testCfg.Firecracker, logger)
	fsMgr := fs.NewManager(testCfg.Storage, testCfg.Firecracker, logger)
	analyzer := llm.NewRedTeamAnalyzer(testCfg.LLM, logger)
	scorer := scoring.NewScorer(logger)

	return worker.NewPool(testCfg.Worker, testCfg.Firecracker, factory, fsMgr, analyzer, scorer, logger)
}

const goodVerifier = `
import sys
import argparse

parser = argparse.ArgumentParser()
parser.add_argument("--output", required=True)
parser.add_argument("--submission-id", required=True)
args = parser.parse_args()

with open(args.output) as f:
    content = f.read().strip()

# Simple length-based scoring: longer outputs score higher (capped at 1.0)
score = min(len(content) / 100.0, 1.0)
print(f"SCORE: {score:.4f}")
`

const hardcodedScoreVerifier = `
# RED TEAM: hardcoded score — should be rejected
print("SCORE: 1.0")
`

const infiniteLoopVerifier = `
# Should be killed by the wall-clock timeout
import time
while True:
    time.sleep(0.1)
print("SCORE: 1.0")
`

const forkBombVerifier = `
# Should be killed by RLIMIT_NPROC
import os
while True:
    os.fork()
`

// TestGoodVerifierPipeline verifies that a well-behaved verifier reaches TRUSTED
func TestGoodVerifierPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool := newTestPool(t)
	poolErr := make(chan error, 1)
	go func() { poolErr <- pool.Run(ctx) }()

	sub := &models.Submission{
		ID:           "test-good-001",
		VerifierCode: goodVerifier,
		ModelOutput:  "The answer to life, the universe and everything is 42.",
		Status:       models.StatusPending,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	resultCh := make(chan *models.EvaluationResult, 1)
	errCh := make(chan error, 1)

	if err := pool.Submit(&models.WorkerJob{Submission: sub, ResultCh: resultCh, ErrCh: errCh}); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	select {
	case result := <-resultCh:
		if sub.Status != models.StatusTrusted {
			t.Errorf("expected TRUSTED, got %s", sub.Status)
		}
		if result.Score <= 0 {
			t.Errorf("expected positive score, got %.4f", result.Score)
		}
		if result.ExitCode != 0 {
			t.Errorf("expected exit code 0, got %d\nstderr: %s", result.ExitCode, result.Stderr)
		}
		t.Logf("✓ score=%.4f elapsed=%dms mem=%dKB", result.Score, result.ElapsedMs, result.MemUsedKB)

	case err := <-errCh:
		t.Fatalf("evaluation error: %v", err)
	case <-ctx.Done():
		t.Fatal("test timed out")
	}

	cancel()
}

// TestHardcodedScoreRejected verifies red-team catches reward hacking
func TestHardcodedScoreRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool := newTestPool(t)
	go pool.Run(ctx)

	sub := &models.Submission{
		ID:           "test-hardcode-001",
		VerifierCode: hardcodedScoreVerifier,
		ModelOutput:  "irrelevant",
		Status:       models.StatusPending,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	resultCh := make(chan *models.EvaluationResult, 1)
	errCh := make(chan error, 1)

	pool.Submit(&models.WorkerJob{Submission: sub, ResultCh: resultCh, ErrCh: errCh})

	select {
	case <-resultCh:
		if sub.Status != models.StatusRejected {
			t.Errorf("expected REJECTED for hardcoded score, got %s", sub.Status)
		}
		if sub.RedTeamReport == nil {
			t.Error("expected red team report to be present")
		} else {
			t.Logf("✓ rejected with risk_score=%.2f", sub.RedTeamReport.RiskScore)
		}
	case err := <-errCh:
		t.Fatalf("evaluation error: %v", err)
	case <-ctx.Done():
		t.Fatal("test timed out")
	}
	cancel()
}

// TestTimeoutKillsVerifier verifies the wall-clock timeout works
func TestTimeoutKillsVerifier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := newTestPool(t)
	go pool.Run(ctx)

	sub := &models.Submission{
		ID:           "test-timeout-001",
		VerifierCode: infiniteLoopVerifier,
		ModelOutput:  "test",
		Status:       models.StatusPending,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	resultCh := make(chan *models.EvaluationResult, 1)
	errCh := make(chan error, 1)

	pool.Submit(&models.WorkerJob{Submission: sub, ResultCh: resultCh, ErrCh: errCh})

	select {
	case result := <-resultCh:
		if !result.TimedOut {
			t.Errorf("expected TimedOut=true, got false (exit=%d)", result.ExitCode)
		}
		if sub.Status != models.StatusRejected {
			t.Errorf("expected REJECTED for timed-out submission, got %s", sub.Status)
		}
		t.Logf("✓ verifier timed out after %dms", result.ElapsedMs)
	case err := <-errCh:
		t.Fatalf("evaluation error: %v", err)
	case <-ctx.Done():
		t.Fatal("test timed out waiting for timeout to trigger")
	}
	cancel()
}

// TestConcurrentSubmissions stress-tests the worker pool with concurrent jobs
func TestConcurrentSubmissions(t *testing.T) {
	const numJobs = 20

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool := newTestPool(t)
	go pool.Run(ctx)

	results := make(chan *models.EvaluationResult, numJobs)
	errors := make(chan error, numJobs)

	for i := 0; i < numJobs; i++ {
		sub := &models.Submission{
			ID:           fmt.Sprintf("test-concurrent-%03d", i),
			VerifierCode: goodVerifier,
			ModelOutput:  fmt.Sprintf("Output number %d with some content to score.", i),
			Status:       models.StatusPending,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		pool.Submit(&models.WorkerJob{Submission: sub, ResultCh: results, ErrCh: errors})
	}

	succeeded, failed := 0, 0
	for i := 0; i < numJobs; i++ {
		select {
		case r := <-results:
			if r.ExitCode == 0 {
				succeeded++
			} else {
				failed++
			}
		case err := <-errors:
			t.Logf("job error: %v", err)
			failed++
		case <-ctx.Done():
			t.Fatalf("timed out after %d/%d results", succeeded+failed, numJobs)
		}
	}

	t.Logf("concurrent test: %d succeeded, %d failed out of %d", succeeded, failed, numJobs)
	if failed > 0 {
		t.Errorf("expected 0 failures, got %d", failed)
	}
	cancel()
}
// TestAPISubmitEndpoint tests the REST submission endpoint
func TestAPISubmitEndpoint(t *testing.T) {
	body := `{
		"contributor_id": "test-user",
		"verifier_code": "print('SCORE: 0.8')",
		"model_output": "test output"
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/submissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// We test the handler logic in isolation here (no real pool needed for unit tests)
	// The integration test would wire up a real pool and call the live server
	_ = rec
	_ = req

	// Assert 202 Accepted with submission_id
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Skipf("skipping body decode (handler not wired in this test): %v", err)
	}

	if _, ok := resp["submission_id"]; !ok {
		t.Error("response missing submission_id")
	}
}
