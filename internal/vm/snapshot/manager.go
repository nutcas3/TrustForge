package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/google/uuid"
	pkgconfig "github.com/nutcas3/trustforge/pkg/config"
	tfmodels "github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

// Manager manages VM snapshots
type Manager struct {
	cfg           pkgconfig.FirecrackerConfig
	logger        *logrus.Logger
	mu            sync.RWMutex
	maxWarmSnaps  int
	warmSnapshots []*tfmodels.VMSnapshot
}

// NewManager creates a new snapshot manager
func NewManager(cfg pkgconfig.FirecrackerConfig, logger *logrus.Logger) *Manager {
	return &Manager{
		cfg:          cfg,
		logger:       logger,
		maxWarmSnaps: 10, // Default limit to prevent memory leaks
	}
}

// WarmUpSnapshot boots a base VM and takes a memory snapshot once Python is ready.
// This is called once at startup (or periodically) to pre-bake the warm pool.
//
// The flow:
//  1. Boot a VM with only the base.ext4 (no task disk yet)
//  2. Wait for the guest agent to signal "PYTHON_READY" via vsock
//  3. Pause the VM and call the Firecracker snapshot API
//  4. Store snapshot metadata in the warm pool
func (m *Manager) WarmUpSnapshot(ctx context.Context, lifecycle LifecycleManager, configBuilder ConfigBuilder) (*tfmodels.VMSnapshot, error) {
	snapID := uuid.New().String()
	logger := m.logger.WithField("snap_id", snapID)
	logger.Info("warming up snapshot: booting base VM")

	socketPath := filepath.Join(m.cfg.SocketDir, fmt.Sprintf("snap-%s.sock", snapID))
	memFile := filepath.Join(m.cfg.SnapshotDir, fmt.Sprintf("%s.mem", snapID))
	snapFile := filepath.Join(m.cfg.SnapshotDir, fmt.Sprintf("%s.snap", snapID))

	// Build the machine config — base image only, no task disk yet
	cfg := configBuilder.BuildBaseConfig(snapID, socketPath, "")

	machine, err := lifecycle.StartVM(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating machine for snapshot: %w", err)
	}

	if err := machine.Start(ctx); err != nil {
		lifecycle.StopVM(ctx, machine)
		return nil, fmt.Errorf("starting base VM for snapshot: %w", err)
	}

	logger.Info("base VM booted, waiting for Python ready signal")

	// Wait for the guest agent to report readiness via vsock
	// (vsock package handles this — see internal/vsock)
	if err := waitForGuestReady(ctx, snapID, m.cfg.ExecutionTimeout); err != nil {
		lifecycle.StopVM(ctx, machine)
		return nil, fmt.Errorf("waiting for guest ready: %w", err)
	}

	// Pause VM before snapshotting
	if err := lifecycle.PauseVM(ctx, machine); err != nil {
		lifecycle.StopVM(ctx, machine)
		return nil, fmt.Errorf("pausing VM for snapshot: %w", err)
	}

	// Take the memory + microvm snapshot
	err = machine.CreateSnapshot(ctx, memFile, snapFile)
	if err != nil {
		lifecycle.StopVM(ctx, machine)
		return nil, fmt.Errorf("creating snapshot: %w", err)
	}

	lifecycle.StopVM(ctx, machine)
	logger.WithFields(logrus.Fields{
		"mem_file":  memFile,
		"snap_file": snapFile,
	}).Info("snapshot created successfully")

	snap := &tfmodels.VMSnapshot{
		ID:           snapID,
		MemFilePath:  memFile,
		SnapFilePath: snapFile,
		CreatedAt:    time.Now(),
		PythonReady:  true,
	}

	// Add to warm pool with cleanup
	if err := m.addSnapshot(snap, logger); err != nil {
		return nil, fmt.Errorf("adding snapshot to pool: %w", err)
	}

	return snap, nil
}

// AcquireWarmSnapshot pops a snapshot from the warm pool (FIFO).
// If the pool is empty, returns nil (caller must cold-boot or wait).
func (m *Manager) AcquireWarmSnapshot() *tfmodels.VMSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.warmSnapshots) == 0 {
		return nil
	}
	snap := m.warmSnapshots[0]
	m.warmSnapshots = m.warmSnapshots[1:]
	return snap
}

// WarmSnapshotCount returns the current number of warm snapshots available
func (m *Manager) WarmSnapshotCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.warmSnapshots)
}

// addSnapshot adds a snapshot to the warm pool with cleanup of old snapshots
func (m *Manager) addSnapshot(snap *tfmodels.VMSnapshot, logger *logrus.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Cleanup old snapshots if we're at the limit
	if len(m.warmSnapshots) >= m.maxWarmSnaps {
		// Remove oldest snapshot (FIFO)
		oldSnap := m.warmSnapshots[0]
		m.warmSnapshots = m.warmSnapshots[1:]

		// Clean up snapshot files (ignore errors, they're just cleanup)
		if err := os.Remove(oldSnap.MemFilePath); err != nil {
			logger.WithError(err).WithField("old_snap_id", oldSnap.ID).Warn("failed to remove old memory file")
		}
		if err := os.Remove(oldSnap.SnapFilePath); err != nil {
			logger.WithError(err).WithField("old_snap_id", oldSnap.ID).Warn("failed to remove old snapshot file")
		}
		logger.WithField("old_snap_id", oldSnap.ID).Info("removed old warm snapshot")
	}
	m.warmSnapshots = append(m.warmSnapshots, snap)
	return nil
}

// waitForGuestReady waits for the guest agent to signal it is initialized.
// In production this reads from the vsock. Here we show the timeout pattern.
func waitForGuestReady(ctx context.Context, vmID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The real implementation uses the vsock package (internal/vsock)
	// to read a "READY\n" message from the guest agent.
	// See internal/vsock/host.go for the full implementation.
	select {
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for guest ready signal (vm: %s)", vmID)
	case <-guestReadyChan(vmID):
		return nil
	}
}

// guestReadyChan is a placeholder — the real vsock listener is in internal/vsock
func guestReadyChan(vmID string) <-chan struct{} {
	ch := make(chan struct{})
	// Wired to vsock.HostListener in the full implementation
	return ch
}

// LifecycleManager interface for VM lifecycle operations
type LifecycleManager interface {
	StartVM(ctx context.Context, cfg firecracker.Config) (*firecracker.Machine, error)
	StopVM(ctx context.Context, machine *firecracker.Machine) error
	PauseVM(ctx context.Context, machine *firecracker.Machine) error
}

// ConfigBuilder interface for VM configuration
type ConfigBuilder interface {
	BuildBaseConfig(vmID, socketPath, taskDiskPath string) firecracker.Config
}
