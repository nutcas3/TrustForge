// Package worker implements the concurrent VM lifecycle orchestrator.
// It manages a pool of Firecracker VMs using golang.org/x/sync/errgroup
// and maintains a warm snapshot pool for sub-5ms resume times.
package worker

import (
	"context"
	"fmt"

	"github.com/nutcas3/trustforge/internal/fs"
	"github.com/nutcas3/trustforge/internal/llm"
	"github.com/nutcas3/trustforge/internal/scoring"
	"github.com/nutcas3/trustforge/internal/vm"
	"github.com/nutcas3/trustforge/internal/worker/metrics"
	"github.com/nutcas3/trustforge/internal/worker/processor"
	"github.com/nutcas3/trustforge/internal/worker/scheduler"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Pool orchestrates concurrent MicroVM evaluations
type Pool struct {
	cfg       config.WorkerConfig
	fcCfg     config.FirecrackerConfig
	jobQueue  chan *models.WorkerJob
	logger    *logrus.Logger
	collector *metrics.Collector
	processor *processor.Processor
	scheduler *scheduler.SnapshotScheduler
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
	collector := metrics.NewCollector()

	// Create processor dependencies
	deps := processor.Dependencies{
		Factory:  factory,
		FsMgr:    fsMgr,
		Analyzer: analyzer,
		Scorer:   scorer,
		Logger:   logger,
	}

	proc := processor.NewProcessor(fcCfg, deps, collector)
	sched := scheduler.NewSnapshotScheduler(factory, cfg, logger)

	return &Pool{
		cfg:       cfg,
		fcCfg:     fcCfg,
		jobQueue:  make(chan *models.WorkerJob, cfg.QueueDepth),
		logger:    logger,
		collector: collector,
		processor: proc,
		scheduler: sched,
	}
}

// Submit enqueues a submission for evaluation.
// Returns an error if the queue is full.
func (p *Pool) Submit(job *models.WorkerJob) error {
	select {
	case p.jobQueue <- job:
		p.collector.IncrementTotalJobs()
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
	if err := p.scheduler.PreWarmSnapshots(ctx); err != nil {
		return fmt.Errorf("pre-warming snapshots: %w", err)
	}

	g, gCtx := errgroup.WithContext(ctx)

	// Launch worker goroutines
	for i := 0; i < p.cfg.PoolSize; i++ {
		worker := scheduler.NewWorkerRunner(i, p.processor, p.logger)
		g.Go(func() error {
			return worker.Run(gCtx, p.jobQueue)
		})
	}

	// Launch snapshot replenisher
	g.Go(func() error {
		return p.scheduler.ReplenishSnapshots(gCtx)
	})

	p.logger.Info("worker pool running")
	return g.Wait()
}

// Metrics returns current pool statistics
func (p *Pool) Metrics() metrics.Metrics {
	m := p.collector.GetMetrics(len(p.jobQueue), 0) // TODO: Get warm snap count from factory
	return metrics.Metrics{
		ActiveVMs:  m.ActiveVMs,
		TotalJobs:  m.TotalJobs,
		FailedJobs: m.FailedJobs,
		QueueDepth: m.QueueDepth,
		WarmSnaps:  m.WarmSnaps,
	}
}
