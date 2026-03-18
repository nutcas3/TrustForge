package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/nutcas3/trustforge/internal/vm"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/sirupsen/logrus"
)

// SnapshotScheduler manages snapshot replenishment
type SnapshotScheduler struct {
	factory *vm.Factory
	cfg     config.WorkerConfig
	logger  *logrus.Logger
}

// NewSnapshotScheduler creates a new snapshot scheduler
func NewSnapshotScheduler(factory *vm.Factory, cfg config.WorkerConfig, logger *logrus.Logger) *SnapshotScheduler {
	return &SnapshotScheduler{
		factory: factory,
		cfg:     cfg,
		logger:  logger,
	}
}

// PreWarmSnapshots initializes the warm snapshot pool
func (s *SnapshotScheduler) PreWarmSnapshots(ctx context.Context) error {
	s.logger.WithField("target_count", s.cfg.WarmSnapshotCount).Info("pre-warming snapshot pool")

	for i := 0; i < s.cfg.WarmSnapshotCount; i++ {
		snap, err := s.factory.WarmUpSnapshot(ctx)
		if err != nil {
			return fmt.Errorf("warming snapshot %d: %w", i, err)
		}
		s.logger.WithField("snap_id", snap.ID).Info("snapshot warmed up")
	}

	s.logger.Info("snapshot pool pre-warming complete")
	return nil
}

// ReplenishSnapshots continuously maintains the warm snapshot pool
func (s *SnapshotScheduler) ReplenishSnapshots(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("snapshot replenisher shutting down")
			return nil
		default:
			if s.factory.WarmSnapshotCount() < s.cfg.WarmSnapshotCount {
				snap, err := s.factory.WarmUpSnapshot(ctx)
				if err != nil {
					s.logger.WithError(err).Error("failed to warm up snapshot")
					// Continue trying, don't fail the whole scheduler
				} else {
					s.logger.WithField("snap_id", snap.ID).Debug("added new warm snapshot")
				}
			}
			// Wait before checking again
			if err := s.sleepWithContext(ctx, 5*time.Second); err != nil {
				return err
			}
		}
	}
}

// sleepWithContext sleeps with context cancellation support
func (s *SnapshotScheduler) sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
