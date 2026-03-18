package lifecycle

import (
	"context"
	"fmt"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/sirupsen/logrus"
)

// Manager handles VM lifecycle operations
type Manager struct {
	logger *logrus.Logger
}

// NewManager creates a new lifecycle manager
func NewManager(logger *logrus.Logger) *Manager {
	return &Manager{logger: logger}
}

// StartVM creates and starts a new Firecracker machine
func (m *Manager) StartVM(ctx context.Context, cfg firecracker.Config) (*firecracker.Machine, error) {
	machine, err := firecracker.NewMachine(ctx, cfg, firecracker.WithLogger(logrus.NewEntry(m.logger)))
	if err != nil {
		return nil, fmt.Errorf("creating machine: %w", err)
	}
	return machine, nil
}

// StopVM stops a Firecracker machine
func (m *Manager) StopVM(ctx context.Context, machine *firecracker.Machine) error {
	if err := machine.StopVMM(); err != nil {
		return fmt.Errorf("stopping VM: %w", err)
	}
	return nil
}

// PauseVM pauses a Firecracker machine for snapshotting
func (m *Manager) PauseVM(ctx context.Context, machine *firecracker.Machine) error {
	if err := machine.PauseVM(ctx); err != nil {
		return fmt.Errorf("pausing VM: %w", err)
	}
	return nil
}

// ResumeVM resumes a paused Firecracker machine
func (m *Manager) ResumeVM(ctx context.Context, machine *firecracker.Machine) error {
	if err := machine.ResumeVM(ctx); err != nil {
		return fmt.Errorf("resuming VM: %w", err)
	}
	return nil
}
