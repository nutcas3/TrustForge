package vm

import (
	"context"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	tfmodels "github.com/nutcas3/trustforge/pkg/models"
)

// VMInstance represents a single running Firecracker VM
type VMInstance struct {
	ID           string
	SubmissionID string
	Machine      *firecracker.Machine
	SocketPath   string
	StartedAt    time.Time
	SnapshotID   string // non-empty if resumed from snapshot
}

// VMFactory interface for VM operations
type VMFactory interface {
	WarmUpSnapshot(ctx context.Context) (*tfmodels.VMSnapshot, error)
	ResumeFromSnapshot(ctx context.Context, submissionID string, taskDiskPath string, snap *tfmodels.VMSnapshot) (*VMInstance, error)
	Stop(ctx context.Context, vm *VMInstance) error
	AcquireWarmSnapshot() *tfmodels.VMSnapshot
	WarmSnapshotCount() int
}

// SnapshotManager interface for snapshot operations
type SnapshotManager interface {
	CreateSnapshot(ctx context.Context, snapID string) (*tfmodels.VMSnapshot, error)
	CleanupOldSnapshots() error
	AcquireSnapshot() *tfmodels.VMSnapshot
	GetSnapshotCount() int
}

// ConfigBuilder interface for VM configuration
type ConfigBuilder interface {
	BuildBaseConfig(vmID, socketPath, taskDiskPath string) firecracker.Config
	BuildResumeConfig(vmID, socketPath string, snap *tfmodels.VMSnapshot, taskDiskPath string) firecracker.Config
}

// LifecycleManager interface for VM lifecycle
type LifecycleManager interface {
	StartVM(ctx context.Context, cfg firecracker.Config) (*firecracker.Machine, error)
	StopVM(ctx context.Context, machine *firecracker.Machine) error
	PauseVM(ctx context.Context, machine *firecracker.Machine) error
}
