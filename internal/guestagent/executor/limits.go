package executor

import (
	"fmt"
	"syscall"
)

// Resource limits for verifier execution
const (
	CPULimitSoft      = 20  // seconds
	CPULimitHard      = 25  // seconds
	MemLimitMB        = 512 // MB
	FileSizeLimitMB   = 10  // MB
	MaxOpenFiles      = 64
	WallTimeoutSec    = 30 // seconds (longer than CPU hard limit)
)

// SetResourceLimits sets resource limits for the current process
func SetResourceLimits() error {
	// CPU time limit: 20s soft, 25s hard
	if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{Cur: CPULimitSoft, Max: CPULimitHard}); err != nil {
		return fmt.Errorf("set CPU limit: %w", err)
	}

	// Virtual memory: 512MB
	if err := syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: uint64(MemLimitMB) * 1024 * 1024, Max: uint64(MemLimitMB) * 1024 * 1024}); err != nil {
		return fmt.Errorf("set memory limit: %w", err)
	}

	// Max file size: 10MB
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: uint64(FileSizeLimitMB) * 1024 * 1024, Max: uint64(FileSizeLimitMB) * 1024 * 1024}); err != nil {
		return fmt.Errorf("set file size limit: %w", err)
	}

	// Max open file descriptors: 64
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: uint64(MaxOpenFiles), Max: uint64(MaxOpenFiles)}); err != nil {
		return fmt.Errorf("set nofile limit: %w", err)
	}

	return nil
}
