// Package vm manages the lifecycle of Firecracker MicroVMs.
// It implements the "Instant-Boot" snapshot strategy for sub-5ms resume times.
package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nutcas3/trustforge/internal/vm/config"
	"github.com/nutcas3/trustforge/internal/vm/lifecycle"
	"github.com/nutcas3/trustforge/internal/vm/snapshot"
	pkgconfig "github.com/nutcas3/trustforge/pkg/config"
	tfmodels "github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

// Factory creates and manages Firecracker MicroVM instances
type Factory struct {
	cfg           pkgconfig.FirecrackerConfig
	logger        *logrus.Logger
	snapManager   *snapshot.Manager
	configBuilder *config.Builder
	lifecycle     *lifecycle.Manager
}

// NewFactory initializes the VM Factory
func NewFactory(cfg pkgconfig.FirecrackerConfig, logger *logrus.Logger) *Factory {
	return &Factory{
		cfg:           cfg,
		logger:        logger,
		snapManager:   snapshot.NewManager(cfg, logger),
		configBuilder: config.NewBuilder(cfg),
		lifecycle:     lifecycle.NewManager(logger),
	}
}

// WarmUpSnapshot boots a base VM and takes a memory snapshot once Python is ready.
func (f *Factory) WarmUpSnapshot(ctx context.Context) (*tfmodels.VMSnapshot, error) {
	return f.snapManager.WarmUpSnapshot(ctx, f.lifecycle, f.configBuilder)
}

// ResumeFromSnapshot starts a new VM by resuming from a warm snapshot.
// The unique task disk (containing verifier.py) is injected as /dev/vdb.
//
// Resume time target: <5ms (vs 125ms cold boot)
func (f *Factory) ResumeFromSnapshot(
	ctx context.Context,
	submissionID string,
	taskDiskPath string,
	snap *tfmodels.VMSnapshot,
) (*VMInstance, error) {
	vmID := uuid.New().String()
	logger := f.logger.WithFields(logrus.Fields{
		"vm_id":         vmID,
		"submission_id": submissionID,
		"snap_id":       snap.ID,
	})

	socketPath := f.cfg.SocketDir + "/" + vmID + ".sock"

	// Build resume config with snapshot and task disk
	cfg := f.configBuilder.BuildResumeConfig(vmID, socketPath, snap, taskDiskPath)

	machine, err := f.lifecycle.StartVM(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating VM from snapshot: %w", err)
	}

	// Resume from snapshot (sub-5ms)
	if err := machine.Start(ctx); err != nil {
		f.lifecycle.StopVM(ctx, machine)
		return nil, fmt.Errorf("resuming VM from snapshot: %w", err)
	}

	logger.Info("VM resumed from snapshot")

	return &VMInstance{
		ID:           vmID,
		SubmissionID: submissionID,
		Machine:      machine,
		SocketPath:   socketPath,
		StartedAt:    time.Now(),
		SnapshotID:   snap.ID,
	}, nil
}

// Stop gracefully shuts down a VM instance
func (f *Factory) Stop(ctx context.Context, vm *VMInstance) error {
	logger := f.logger.WithFields(logrus.Fields{
		"vm_id":         vm.ID,
		"submission_id": vm.SubmissionID,
	})

	logger.Info("stopping VM")
	if err := f.lifecycle.StopVM(ctx, vm.Machine); err != nil {
		return fmt.Errorf("stopping VM %s: %w", vm.ID, err)
	}

	logger.Info("VM stopped successfully")
	return nil
}

// AcquireWarmSnapshot pops a snapshot from the warm pool (FIFO).
// If the pool is empty, returns nil (caller must cold-boot or wait).
func (f *Factory) AcquireWarmSnapshot() *tfmodels.VMSnapshot {
	return f.snapManager.AcquireWarmSnapshot()
}

// WarmSnapshotCount returns the current number of warm snapshots available
func (f *Factory) WarmSnapshotCount() int {
	return f.snapManager.WarmSnapshotCount()
}
