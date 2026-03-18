package executor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	TaskDevice     = "/dev/vdb"
	TaskMount      = "/task"
	MaxOutputBytes = 1 * 1024 * 1024 // 1MB cap on stdout/stderr
)

// cappedBuffer is a write-only buffer that silently discards writes after limit bytes.
type cappedBuffer struct {
	data  []byte
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.data)
	if remaining <= 0 {
		return len(p), nil // silently drop
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *cappedBuffer) String() string { return string(b.data) }

// WriteTo satisfies io.WriterTo (used internally by exec.Cmd)
func (b *cappedBuffer) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(b.data)
	return int64(n), err
}

// RunVerifier executes the verifier script with resource constraints
func RunVerifier(submissionID string) *Result {
	result := NewResult(submissionID)

	verifierPath := filepath.Join(TaskMount, "verifier.py")
	if _, err := os.Stat(verifierPath); err != nil {
		result.Stderr = fmt.Sprintf("verifier.py not found at %s: %v", verifierPath, err)
		result.ExitCode = 127
		return result
	}

	start := time.Now()

	cmd := exec.Command(
		"python3", verifierPath,
		"--output", filepath.Join(TaskMount, "output.txt"),
		"--submission-id", submissionID,
	)

	// New process group so we can kill the whole tree at once
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Set resource limits - these will be inherited by child process
	if err := SetResourceLimits(); err != nil {
		logf("failed to set resource limits: %v", err)
	}

	var stdoutBuf, stderrBuf cappedBuffer
	stdoutBuf.limit = MaxOutputBytes
	stderrBuf.limit = MaxOutputBytes
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Wall-clock timeout kills the process group if Python hangs
	var timedOut int32
	timer := time.AfterFunc(WallTimeoutSec*time.Second, func() {
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			atomic.StoreInt32(&timedOut, 1)
		}
	})
	defer timer.Stop()

	runErr := cmd.Run()

	result.Stdout = stdoutBuf.String()
	result.Stderr = stderrBuf.String()
	result.TimedOut = atomic.LoadInt32(&timedOut) == 1

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			if ru, ok := exitErr.ProcessState.SysUsage().(*syscall.Rusage); ok {
				result.MemUsedKB = ru.Maxrss
			}
		} else {
			result.ExitCode = 1
			result.Stderr += "\n[guest-agent error]: " + runErr.Error()
		}
	} else {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			result.MemUsedKB = ru.Maxrss
		}
	}

	result.Finalize(start)
	return result
}

// logf is a simple logging function (will be replaced with proper logger)
func logf(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[guest-agent %s] "+format+"\n", append([]any{ts}, args...)...)
}
