package scheduler

import (
	"context"

	"github.com/nutcas3/trustforge/internal/worker/processor"
	"github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

// WorkerRunner manages individual worker goroutines
type WorkerRunner struct {
	id        int
	processor *processor.Processor
	logger    *logrus.Entry
}

// NewWorkerRunner creates a new worker runner
func NewWorkerRunner(id int, proc *processor.Processor, logger *logrus.Logger) *WorkerRunner {
	return &WorkerRunner{
		id:        id,
		processor: proc,
		logger:    logger.WithField("worker_id", id),
	}
}

// Run starts the worker event loop
func (w *WorkerRunner) Run(ctx context.Context, jobQueue <-chan *models.WorkerJob) error {
	w.logger.Debug("worker started")

	for {
		select {
		case <-ctx.Done():
			w.logger.Debug("worker shutting down")
			return nil
		case job := <-jobQueue:
			if err := w.processor.EvaluateSubmission(ctx, job); err != nil {
				w.logger.WithError(err).WithField("submission_id", job.Submission.ID).
					Error("evaluation failed")
				// Report error back to caller
				select {
				case job.ErrCh <- err:
				default:
					w.logger.Warn("error channel blocked, error dropped")
				}
			}
		}
	}
}
