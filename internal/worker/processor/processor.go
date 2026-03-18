package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/nutcas3/trustforge/internal/fs"
	"github.com/nutcas3/trustforge/internal/llm"
	"github.com/nutcas3/trustforge/internal/scoring"
	"github.com/nutcas3/trustforge/internal/vm"
	"github.com/nutcas3/trustforge/internal/vsock"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

// Dependencies for job processing
type Dependencies struct {
	Factory  *vm.Factory
	FsMgr    *fs.Manager
	Analyzer *llm.RedTeamAnalyzer
	Scorer   *scoring.Scorer
	Logger   *logrus.Logger
}

// Processor handles the evaluation pipeline for submissions
type Processor struct {
	cfg      config.FirecrackerConfig
	deps     Dependencies
	metrics  MetricsTracker
}

// MetricsTracker interface for updating metrics
type MetricsTracker interface {
	IncrementActiveVMs()
	DecrementActiveVMs()
	IncrementTotalJobs()
	IncrementFailedJobs()
}

// NewProcessor creates a new job processor
func NewProcessor(cfg config.FirecrackerConfig, deps Dependencies, metrics MetricsTracker) *Processor {
	return &Processor{
		cfg:     cfg,
		deps:    deps,
		metrics: metrics,
	}
}

// EvaluateSubmission runs the full evaluation pipeline for a submission
// 1. Create ephemeral task disk
// 2. Resume VM from snapshot (or cold boot)
// 3. Run verifier via vsock
// 4. Stop VM, delete task disk
// 5. Red-team analysis
// 6. Scoring + trust decision
func (p *Processor) EvaluateSubmission(ctx context.Context, job *models.WorkerJob) error {
	sub := job.Submission
	logger := p.deps.Logger.WithField("submission_id", sub.ID)

	start := time.Now()
	p.metrics.IncrementActiveVMs()
	defer p.metrics.DecrementActiveVMs()

	// --- Stage 1: Sandbox Prep ---
	sub.Status = models.StatusSandboxing
	logger.Info("stage: sandbox prep")

	taskDisk, err := p.deps.FsMgr.CreateTaskDisk(sub.ID, sub.VerifierCode, sub.ModelOutput)
	if err != nil {
		return fmt.Errorf("creating task disk: %w", err)
	}
	defer func() {
		if err := p.deps.FsMgr.RemoveTaskDisk(taskDisk); err != nil {
			logger.WithError(err).Error("failed to remove task disk")
		}
	}()

	sub.Status = models.StatusRunning
	logger.Info("stage: vm execution")

	execCtx, cancel := context.WithTimeout(ctx, p.cfg.ExecutionTimeout)
	defer cancel()

	// Acquire a warm snapshot or fall back to cold boot
	snap := p.deps.Factory.AcquireWarmSnapshot()
	var vmInst *vm.VMInstance

	if snap != nil {
		logger.WithField("snapshot_id", snap.ID).Debug("resuming from warm snapshot")
		vmInst, err = p.deps.Factory.ResumeFromSnapshot(execCtx, sub.ID, taskDisk.Path, snap)
	} else {
		logger.Warn("no warm snapshots available — cold boot (slower)")
		// Cold boot path would go here
		return fmt.Errorf("no warm snapshots available and cold boot not implemented")
	}

	if err != nil {
		return fmt.Errorf("starting VM: %w", err)
	}

	defer func() {
		if err := p.deps.Factory.Stop(ctx, vmInst); err != nil {
			logger.WithError(err).Error("failed to stop VM")
		}
	}()

	// Communicate with the guest via vsock
	vsockPath := p.cfg.SocketDir + "/vsock-" + vmInst.ID + ".sock"
	vsockClient := vsock.NewHostClient(vmInst.ID, vsockPath, p.deps.Logger)

	evalResult, err := vsockClient.RunVerifier(execCtx, sub.ID)
	if err != nil {
		return fmt.Errorf("running verifier: %w", err)
	}

	// --- Stage 3: Red-Team Analysis ---
	sub.Status = models.StatusRedTeam
	logger.Info("stage: red-team analysis")

	redTeamReport, err := p.deps.Analyzer.Analyze(ctx, sub)
	if err != nil {
		// Red-team failure is non-fatal but logged
		logger.WithError(err).Warn("red-team analysis failed, treating as suspicious")
		// Create a failed report
		riskScore := 1.0
		redTeamReport = &models.RedTeamReport{
			RiskScore:      riskScore,
			Recommendation: "REJECT",
			PassedRedTeam:  false,
		}
	}

	sub.Status = models.StatusRunning // reset before scoring sets final status
	logger.Info("stage: scoring")

	if err := p.deps.Scorer.ScoreSubmission(ctx, sub, evalResult, redTeamReport); err != nil {
		return fmt.Errorf("scoring submission: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"final_status": sub.Status,
		"score":        sub.Score,
		"elapsed_ms":   time.Since(start).Milliseconds(),
	}).Info("evaluation pipeline complete")

	// Send result back to caller
	select {
	case job.ResultCh <- evalResult:
	default:
		logger.Warn("result channel blocked, result dropped")
	}

	return nil
}
