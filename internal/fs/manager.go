// Package fs handles dynamic creation of ephemeral ext4 task disks
// for Firecracker MicroVM isolation.
package fs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/nutcas3/trustforge/pkg/config"
	"github.com/sirupsen/logrus"
)

// Manager handles the lifecycle of ephemeral task disks
type Manager struct {
	cfg    config.StorageConfig
	fcCfg  config.FirecrackerConfig
	logger *logrus.Logger
}

// NewManager creates a new filesystem Manager
func NewManager(cfg config.StorageConfig, fcCfg config.FirecrackerConfig, logger *logrus.Logger) *Manager {
	return &Manager{cfg: cfg, fcCfg: fcCfg, logger: logger}
}

// TaskDisk represents an ephemeral disk created for a single submission
type TaskDisk struct {
	ID           string
	Path         string
	SubmissionID string
	CreatedAt    time.Time
}

// CreateTaskDisk generates a tiny ext4 image for a specific submission.
//
// Strategy:
//   - We create a sparse file (no actual disk blocks allocated until written)
//   - Format as ext4 with a label for traceability
//   - Mount via loop device, write the verifier code and model output
//   - Unmount cleanly — the resulting .ext4 is injected as /dev/vdb in the VM
//
// NOTE: This requires mkfs.ext4, mount, and umount on the host.
// For rootless operation, use the diskfs library instead.
func (m *Manager) CreateTaskDisk(submissionID, verifierCode, modelOutput string) (*TaskDisk, error) {
	diskID := uuid.New().String()
	diskPath := filepath.Join(m.fcCfg.TaskDiskDir, fmt.Sprintf("task-%s.ext4", diskID))

	m.logger.WithFields(logrus.Fields{
		"submission_id": submissionID,
		"disk_path":     diskPath,
		"disk_size_mb":  m.cfg.TaskDiskSize / 1024 / 1024,
	}).Info("creating task disk")

	// Step 1: Create a sparse file
	if err := createSparseFile(diskPath, m.cfg.TaskDiskSize); err != nil {
		return nil, fmt.Errorf("creating sparse file: %w", err)
	}

	// Step 2: Format as ext4
	label := fmt.Sprintf("task-%s", submissionID[:8])
	if err := formatExt4(diskPath, label); err != nil {
		os.Remove(diskPath)
		return nil, fmt.Errorf("formatting ext4: %w", err)
	}

	// Step 3: Mount, populate, unmount
	if err := m.populateDisk(diskPath, submissionID, verifierCode, modelOutput); err != nil {
		os.Remove(diskPath)
		return nil, fmt.Errorf("populating disk: %w", err)
	}

	m.logger.WithField("disk_path", diskPath).Info("task disk ready")

	return &TaskDisk{
		ID:           diskID,
		Path:         diskPath,
		SubmissionID: submissionID,
		CreatedAt:    time.Now(),
	}, nil
}

// RemoveTaskDisk securely deletes an ephemeral task disk
func (m *Manager) RemoveTaskDisk(disk *TaskDisk) error {
	m.logger.WithField("disk_path", disk.Path).Debug("removing task disk")
	if err := os.Remove(disk.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing task disk %s: %w", disk.Path, err)
	}
	return nil
}

// EnsureBaseImageExists validates that the read-only base image is accessible
func (m *Manager) EnsureBaseImageExists() error {
	info, err := os.Stat(m.fcCfg.BaseImagePath)
	if err != nil {
		return fmt.Errorf("base image not found at %s: %w", m.fcCfg.BaseImagePath, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("base image at %s is empty", m.fcCfg.BaseImagePath)
	}
	return nil
}

// EnsureDirectories creates required working directories
func (m *Manager) EnsureDirectories() error {
	dirs := []string{
		m.fcCfg.TaskDiskDir,
		m.fcCfg.SocketDir,
		m.fcCfg.SnapshotDir,
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}
	return nil
}

// createSparseFile creates a sparse file of the given size.
// Sparse files don't allocate real disk blocks until written,
// making them extremely cheap to create.
func createSparseFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("os.Create(%s): %w", path, err)
	}
	defer f.Close()

	// Truncate creates a sparse file — no actual I/O to disk
	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("truncating to %d bytes: %w", size, err)
	}
	return nil
}

// formatExt4 runs mkfs.ext4 on a file path.
// The -F flag forces formatting (no block device check needed).
// The -L flag sets a human-readable label for debugging.
func formatExt4(path, label string) error {
	cmd := exec.Command(
		"mkfs.ext4",
		"-F",        // force (file, not block device)
		"-L", label, // volume label
		"-m", "0", // 0% reserved blocks (no root reservation needed)
		"-E", "lazy_itable_init=0,lazy_journal_init=0", // init fully upfront
		path,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 failed: %w\noutput: %s", err, string(out))
	}
	return nil
}

// populateDisk mounts the disk image, writes the verifier and model output, then unmounts.
// This uses a temporary mount point under /tmp.
//
// PRODUCTION NOTE: For a rootless deployment, replace this with the
// github.com/diskfs/go-diskfs library which can write ext4 without root.
func (m *Manager) populateDisk(diskPath, submissionID, verifierCode, modelOutput string) error {
	mountDir, err := os.MkdirTemp("", fmt.Sprintf("trustforge-mount-%s-*", submissionID[:8]))
	if err != nil {
		return fmt.Errorf("creating temp mount dir: %w", err)
	}
	defer os.RemoveAll(mountDir)

	// Mount the ext4 image via loop device
	mountCmd := exec.Command("mount", "-o", "loop", diskPath, mountDir)
	if out, err := mountCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mounting %s: %w\noutput: %s", diskPath, err, string(out))
	}
	defer func() {
		umountCmd := exec.Command("umount", mountDir)
		if out, err := umountCmd.CombinedOutput(); err != nil {
			m.logger.WithError(err).WithField("output", string(out)).Error("failed to umount task disk")
		}
	}()

	// Write the verifier script
	verifierPath := filepath.Join(mountDir, "verifier.py")
	if err := os.WriteFile(verifierPath, []byte(verifierCode), 0644); err != nil {
		return fmt.Errorf("writing verifier.py: %w", err)
	}

	// Write the model output
	outputPath := filepath.Join(mountDir, "output.txt")
	if err := os.WriteFile(outputPath, []byte(modelOutput), 0644); err != nil {
		return fmt.Errorf("writing output.txt: %w", err)
	}

	// Write a metadata file for the guest agent to consume
	metaPath := filepath.Join(mountDir, "meta.json")
	meta := fmt.Sprintf(`{"submission_id":%q,"created_at":%q}`, submissionID, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(metaPath, []byte(meta), 0644); err != nil {
		return fmt.Errorf("writing meta.json: %w", err)
	}

	return nil
}
