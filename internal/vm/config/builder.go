package config

import (
	"fmt"
	"path/filepath"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	pkgconfig "github.com/nutcas3/trustforge/pkg/config"
	tfmodels "github.com/nutcas3/trustforge/pkg/models"
)

// Builder creates Firecracker VM configurations
type Builder struct {
	cfg pkgconfig.FirecrackerConfig
}

// NewBuilder creates a new configuration builder
func NewBuilder(cfg pkgconfig.FirecrackerConfig) *Builder {
	return &Builder{cfg: cfg}
}

// BuildBaseConfig constructs a Firecracker config for a fresh cold boot (snapshot warm-up).
// taskDiskPath may be empty if not yet needed.
func (b *Builder) BuildBaseConfig(vmID, socketPath, taskDiskPath string) firecracker.Config {
	drives := []models.Drive{
		{
			DriveID:      firecracker.String("rootfs"),
			PathOnHost:   firecracker.String(b.cfg.BaseImagePath),
			IsRootDevice: firecracker.Bool(true),
			IsReadOnly:   firecracker.Bool(true), // Base image is always read-only
		},
	}

	// Attach the task disk as a secondary drive if provided
	if taskDiskPath != "" {
		drives = append(drives, models.Drive{
			DriveID:      firecracker.String("task"),
			PathOnHost:   firecracker.String(taskDiskPath),
			IsRootDevice: firecracker.Bool(false),
			IsReadOnly:   firecracker.Bool(false), // task disk is writable
		})
	}

	return firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: b.cfg.KernelPath,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off",
		Drives:          drives,
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(b.cfg.VCPUCount),
			MemSizeMib: firecracker.Int64(b.cfg.MemSizeMiB),
		},
		// vsock for host<->guest communication without a network interface
		VsockDevices: []firecracker.VsockDevice{
			{
				Path: filepath.Join(b.cfg.SocketDir, fmt.Sprintf("vsock-%s.sock", vmID)),
				CID:  3, // Guest CID — host uses 2, guest uses 3+
			},
		},
		// Jailer configuration for full isolation
		JailerCfg: &firecracker.JailerConfig{
			GID:       firecracker.Int(b.cfg.JailerGID),
			UID:       firecracker.Int(b.cfg.JailerUID),
			ID:        vmID,
			NumaNode:  firecracker.Int(0),
			Daemonize: false,
		},
	}
}

// BuildResumeConfig constructs a Firecracker config that resumes from a snapshot.
// The snapshot files contain the full memory state + microvm state from warm-up.
func (b *Builder) BuildResumeConfig(
	vmID, socketPath string,
	snap *tfmodels.VMSnapshot,
	taskDiskPath string,
) firecracker.Config {
	cfg := b.BuildBaseConfig(vmID, socketPath, taskDiskPath)

	// Override with snapshot resume parameters
	cfg.Snapshot = firecracker.SnapshotConfig{
		MemFilePath:  snap.MemFilePath,
		SnapshotPath: snap.SnapFilePath,
	}

	return cfg
}
