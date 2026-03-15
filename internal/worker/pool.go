// Package worker implements the concurrent VM lifecycle orchestrator.
// It manages a pool of Firecracker VMs using golang.org/x/sync/errgroup
// and maintains a warm snapshot pool for sub-5ms resume times.
package worker

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/nutcas3/trustforge/internal/fs"
	"github.com/nutcas3/trustforge/internal/llm"
	"github.com/nutcas3/trustforge/internal/scoring"
	"github.com/nutcas3/trustforge/internal/vm"
	"github.com/nutcas3/trustforge/internal/vsock"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Pool orchestrates concurrent MicroVM evaluations
type Pool struct {
	cfg      config.WorkerConfig
	fcCfg    config.FirecrackerConfig
	factory  *vm.Factory
	fsMgr    *fs.Manager
	analyzer *llm.RedTeamAnalyzer
	scorer   *scoring.Scorer
	jobQueue chan *models.WorkerJob
	logger   *logrus.Logger

	// Metrics
	activeVMs  atomic.Int64
	totalJobs  atomic.Int64
	failedJobs atomic.Int64
}

// NewPool creates a new worker pool
func NewPool(
	cfg config.WorkerConfig,
	fcCfg config.FirecrackerConfig,
	factory *vm.Factory,
	fsMgr *fs.Manager,
	analyzer *llm.RedTeamAnalyzer,
	scorer *scoring.Scorer,
	logger *logrus.Logger,
) *Pool {
	return &Pool{
		cfg:      cfg,
		fcCfg:    fcCfg,
		factory:  factory,
		fsMgr:    fsMgr,
		analyzer: analyzer,
		scorer:   scorer,
		jobQueue: make(chan *models.WorkerJob, cfg.QueueDepth),
		logger:   logger,
	}
}

// Submit enqueues a submission for evaluation.
// Returns an error if the queue is full.
func (p *Pool) Submit(job *models.WorkerJob) error {
	select {
	case p.jobQueue <- job:
		return nil
	default:
		return fmt.Errorf("worker queue is full (capacity: %d)", p.cfg.QueueDepth)
	}
}

// Run starts the worker pool. It blocks until ctx is cancelled.
// It launches:
//   - cfg.PoolSize workers consuming from the job queue
//   - A snapshot replenisher that keeps the warm pool topped up
func (p *Pool) Run(ctx context.Context) error {
	p.logger.WithFields(logrus.Fields{
		"pool_size":   p.cfg.PoolSize,
		"queue_depth": p.cfg.QueueDepth,
		"warm_snaps":  p.cfg.WarmSnapshotCount,
	}).Info("starting worker pool")

	// Pre-warm the snapshot pool before accepting work
	if err := p.preWarmSnapshots(ctx); err != nil {
		return fmt.Errorf("pre-warming snapshots: %w", err)
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Launch worker goroutines
	for i := 0; i < p.cfg.PoolSize; i++ {
		workerID := i
		g.Go(func() error {
			return p.runWorker(gCtx, workerID)
		})
	}

	// Launch snapshot replenisher
	g.Go(func() error {
		return p.replenishSnapshots(gCtx)
	})

	p.logger.Info("worker pool running")
	return g.Wait()
}

// Metrics returns current pool statistics
type Metrics struct {
	ActiveVMs  int64
	TotalJobs  int64
	FailedJobs int64
	QueueDepth int
	WarmSnaps  int
}

func (p *Pool) Metrics() Metrics {
	return Metrics{
		ActiveVMs:  p.activeVMs.Load(),
		TotalJobs:  p.totalJobs.Load(),
		FailedJobs: p.failedJobs.Load(),
		QueueDepth: len(p.jobQueue),
		WarmSnaps:  p.factory.WarmSnapshotCount(),
	}
}

// runWorker is the per-goroutine event loop.
// It picks jobs from the queue and evaluates them.
func (p *Pool) runWorker(ctx context.Context, id int) error {
	logger := p.logger.WithField("worker_id", id)
	logger.Debug("worker started")

	for {
		select {
		case <-ctx.Done():
			logger.Debug("worker shutting down")
			return nil
		case job := <-p.jobQueue:
			p.totalJobs.Add(1)
			if err := p.evaluateSubmission(ctx, job); err != nil {
				p.failedJobs.Add(1)
				logger.WithError(err).WithField("submission_id", job.Submission.ID).
					Error("evaluation failed")
				// Report error back to caller
				select {
				case job.ErrCh <- err:
				default:
				}
			}
		}
	}
}

// evaluateSubmission is the full pipeline for a single submission:
// 1. Create ephemeral task disk
// 2. Resume VM from snapshot (or cold boot)
// 3. Run verifier via vsock
// 4. Stop VM, delete task disk
// 5. Red-team analysis
// 6. Scoring + trust decision
func (p *Pool) evaluateSubmission(ctx context.Context, job *models.WorkerJob) error {
	sub := job.Submission
	logger := p.logger.WithField("submission_id", sub.ID)

	start := time.Now()
	p.activeVMs.Add(1)
	defer p.activeVMs.Add(-1)

	// --- Stage 1: Sandbox Prep ---
	sub.Status = models.StatusSandboxing
	logger.Info("stage: sandbox prep")

	taskDisk, err := p.fsMgr.CreateTaskDisk(sub.ID, sub.VerifierCode, sub.ModelOutput)
	if err != nil {
		return fmt.Errorf("creating task disk: %w", err)
	}
	defer func() {
		if err := p.fsMgr.RemoveTaskDisk(taskDisk); err != nil {
			logger.WithError(err).Error("failed to remove task disk")
		}
	}()

	sub.Status = models.StatusRunning
	logger.Info("stage: vm execution")

	execCtx, cancel := context.WithTimeout(ctx, p.fcCfg.ExecutionTimeout)
	defer cancel()

	// Acquire a warm snapshot or fall back to cold boot
	snap := p.factory.AcquireWarmSnapshot()
	var vmInst *vm.VMInstance

	if snap != nil {
		logger.WithField("snapshot_id", snap.ID).Debug("resuming from warm snapshot")
		vmInst, err = p.factory.ResumeFromSnapshot(execCtx, sub.ID, taskDisk.Path, snap)
	} else {
		logger.Warn("no warm snapshots available — cold boot (slower)")
		// Cold boot path would go here
		return fmt.Errorf("no warm snapshots available and cold boot not implemented")
	}

	if err != nil {
		return fmt.Errorf("starting VM: %w", err)
	}

	defer func() {
		if err := p.factory.Stop(ctx, vmInst); err != nil {
			logger.WithError(err).Error("failed to stop VM")
		}
	}()

	// Communicate with the guest via vsock
	vsockPath := p.fcCfg.SocketDir + "/vsock-" + vmInst.ID + ".sock"
	vsockClient := vsock.NewHostClient(vmInst.ID, vsockPath, p.logger)

	evalResult, err := vsockClient.RunVerifier(execCtx, sub.ID)
	if err != nil {
		return fmt.Errorf("running verifier: %w", err)
	}

	// --- Stage 3: Red-Team Analysis ---
	sub.Status = models.StatusRedTeam
	logger.Info("stage: red-team analysis")

	redTeamReport, err := p.analyzer.Analyze(ctx, sub)
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

	if err := p.scorer.ScoreSubmission(ctx, sub, evalResult, redTeamReport); err != nil {
		return fmt.Errorf("scoring submission: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"final_status": sub.Status,
		"score":        sub.Score,
		"elapsed_ms":   time.Since(start).Milliseconds(),
	}).Info("evaluation pipeline complete")

	// Deliver result to caller
	select {
	case job.ResultCh <- evalResult:
	default:
		logger.Warn("result channel full, dropping result")
	}

	return nil
}

// preWarmSnapshots boots cfg.WarmSnapshotCount base VMs and snapshots them
func (p *Pool) preWarmSnapshots(ctx context.Context) error {
	p.logger.WithField("count", p.cfg.WarmSnapshotCount).Info("pre-warming snapshot pool")

	g, gCtx := errgroup.WithContext(ctx)

	for i := 0; i < p.cfg.WarmSnapshotCount; i++ {
		g.Go(func() error {
			_, err := p.factory.WarmUpSnapshot(gCtx)
			return err
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("pre-warming snapshots: %w", err)
	}

	p.logger.WithField("count", p.factory.WarmSnapshotCount()).
		Info("snapshot pool ready")
	return nil
}

// replenishSnapshots runs in the background and keeps the warm pool topped up.
// When a snapshot is consumed, this goroutine creates a replacement.
func (p *Pool) replenishSnapshots(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			current := p.factory.WarmSnapshotCount()
			needed := p.cfg.WarmSnapshotCount - current
			if needed <= 0 {
				continue
			}

			p.logger.WithFields(logrus.Fields{
				"current": current,
				"target":  p.cfg.WarmSnapshotCount,
				"needed":  needed,
			}).Debug("replenishing snapshot pool")

			g, gCtx := errgroup.WithContext(ctx)
			for i := 0; i < needed; i++ {
				g.Go(func() error {
					_, err := p.factory.WarmUpSnapshot(gCtx)
					return err
				})
			}

			if err := g.Wait(); err != nil {
				p.logger.WithError(err).Error("snapshot replenishment error")
				// Non-fatal: log and continue
			}
		}
	}
}
