//go:build linux
// +build linux

//   4. Accept exactly ONE RUN command on vsock port 52
//   5. Execute verifier.py under rlimit constraints
//   6. Write JSON result back on the same connection
//   7. Poweroff the VM (Firecracker guest is single-use)

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// vsock address family constant (not in stdlib)
const (
	afVsock        = 40
	vmaddrCIDHost  = 2 // the hypervisor/host
	vmaddrCIDAny   = 0xFFFFFFFF
	commandPort    = 52
	readyPort      = 53
	taskDevice     = "/dev/vdb"
	taskMount      = "/task"
	maxOutputBytes = 1 * 1024 * 1024 // 1MB cap on stdout/stderr
)

// Command is sent from host to guest agent via vsock
type Command struct {
	Type         string `json:"type"`
	SubmissionID string `json:"submission_id"`
}

// Result is sent from guest to host after execution
type Result struct {
	SubmissionID string  `json:"submission_id"`
	ExitCode     int     `json:"exit_code"`
	Stdout       string  `json:"stdout"`
	Stderr       string  `json:"stderr"`
	Score        float64 `json:"score"`
	ElapsedMs    int64   `json:"elapsed_ms"`
	MemUsedKB    int64   `json:"mem_used_kb"`
	TimedOut     bool    `json:"timed_out"`
}

// sockaddrVM is the raw vsock sockaddr for syscall.Bind / syscall.Connect
type sockaddrVM struct {
	Family    uint16
	Reserved1 uint16
	Port      uint32
	CID       uint32
	Pad       [4]byte
}

func main() {
	logf("guest-agent starting (pid %d)", os.Getpid())

	// Mount task disk — may fail during snapshot warm-up (no vdb present)
	if err := mountTaskDisk(); err != nil {
		logf("note: task disk not mounted (%v) — likely snapshot warm-up", err)
	} else {
		logf("task disk mounted at %s", taskMount)
	}

	// Signal host we are ready (triggers snapshot during warm-up)
	if err := signalReady(); err != nil {
		logf("warning: could not signal ready: %v", err)
	}

	// Accept and serve exactly one RUN command then poweroff
	if err := serveOne(); err != nil {
		logf("fatal: %v", err)
		poweroff(1)
	}

	poweroff(0)
}

// setResourceLimits sets resource limits for the current process
func setResourceLimits() error {
	// CPU time limit: 20s soft, 25s hard
	if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{Cur: 20, Max: 25}); err != nil {
		return fmt.Errorf("set CPU limit: %w", err)
	}

	// Virtual memory: 512MB
	if err := syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: 512 * 1024 * 1024, Max: 512 * 1024 * 1024}); err != nil {
		return fmt.Errorf("set memory limit: %w", err)
	}

	// Max file size: 10MB
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 10 * 1024 * 1024, Max: 10 * 1024 * 1024}); err != nil {
		return fmt.Errorf("set file size limit: %w", err)
	}

	// Max open file descriptors: 64
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: 64, Max: 64}); err != nil {
		return fmt.Errorf("set nofile limit: %w", err)
	}

	return nil
}

// vsockDial opens a vsock connection to the host on the given port.
// Uses raw syscalls — stdlib net package has no AF_VSOCK support.
func vsockDial(cid, port uint32) (net.Conn, error) {
	fd, err := syscall.Socket(afVsock, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_VSOCK): %w", err)
	}

	addr := sockaddrVM{
		Family: afVsock,
		Port:   port,
		CID:    cid,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&addr)),
		uintptr(unsafe.Sizeof(addr)),
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("connect(vsock cid=%d port=%d): %w", cid, port, errno)
	}

	// Wrap the raw fd as a net.Conn using os.File
	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", port))
	conn, err := net.FileConn(f)
	f.Close() // FileConn dups the fd, so close the original
	if err != nil {
		return nil, fmt.Errorf("net.FileConn: %w", err)
	}
	return conn, nil
}

// vsockListen creates a vsock server socket bound to the given port.
func vsockListen(port uint32) (net.Listener, error) {
	fd, err := syscall.Socket(afVsock, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_VSOCK): %w", err)
	}

	addr := sockaddrVM{
		Family: afVsock,
		Port:   port,
		CID:    vmaddrCIDAny,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_BIND,
		uintptr(fd),
		uintptr(unsafe.Pointer(&addr)),
		uintptr(unsafe.Sizeof(addr)),
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind(vsock port=%d): %w", port, errno)
	}

	if err := syscall.Listen(fd, 1); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("listen(vsock port=%d): %w", port, err)
	}

	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock-listen:%d", port))
	ln, err := net.FileListener(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("net.FileListener: %w", err)
	}
	return ln, nil
}

// ── Boot Sequence ─────────────────────────────────────────────────────────────

func mountTaskDisk() error {
	if err := os.MkdirAll(taskMount, 0755); err != nil {
		return err
	}
	// Wait up to 1s for the kernel to expose /dev/vdb
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(taskDevice); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(taskDevice); err != nil {
		return fmt.Errorf("%s not present after waiting", taskDevice)
	}
	out, err := exec.Command("mount", "-t", "ext4", "-o", "ro", taskDevice, taskMount).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// signalReady connects to the host via vsock and writes "READY\n".
// The host's WaitForReady() is blocking on this message.
func signalReady() error {
	var (
		conn net.Conn
		err  error
	)
	// Retry — host listener may not be ready yet when the VM boots
	for i := 0; i < 20; i++ {
		conn, err = vsockDial(vmaddrCIDHost, readyPort)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("dialing host ready port after 20 attempts: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprintln(conn, "READY")
	return err
}

// ── Command Server ────────────────────────────────────────────────────────────

// serveOne accepts a single command from the host, processes it, and returns.
func serveOne() error {
	ln, err := vsockListen(commandPort)
	if err != nil {
		return fmt.Errorf("listening on command port %d: %w", commandPort, err)
	}
	defer ln.Close()

	logf("listening for commands on vsock port %d", commandPort)

	// Accept with a generous deadline — host may be queuing
	type accepted struct {
		conn net.Conn
		err  error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, e := ln.Accept()
		ch <- accepted{c, e}
	}()

	select {
	case a := <-ch:
		if a.err != nil {
			return fmt.Errorf("accept: %w", a.err)
		}
		defer a.conn.Close()
		return handleConn(a.conn)
	case <-time.After(120 * time.Second):
		return fmt.Errorf("timed out waiting for host command")
	}
}

func handleConn(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(90 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return fmt.Errorf("connection closed before command received")
	}

	var cmd Command
	if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
		return fmt.Errorf("invalid command JSON: %w", err)
	}

	logf("executing command: type=%s submission=%s", cmd.Type, cmd.SubmissionID)

	switch cmd.Type {
	case "RUN":
		result := runVerifier(cmd.SubmissionID)
		out, _ := json.Marshal(result)
		out = append(out, '\n')
		if _, err := conn.Write(out); err != nil {
			return fmt.Errorf("writing result: %w", err)
		}
		logf("result dispatched: exit=%d score=%.4f elapsed=%dms timed_out=%v",
			result.ExitCode, result.Score, result.ElapsedMs, result.TimedOut)
	case "PING":
		conn.Write([]byte(`{"pong":true}` + "\n"))
	default:
		return fmt.Errorf("unknown command type: %q", cmd.Type)
	}
	return nil
}

// ── Verifier Execution ────────────────────────────────────────────────────────

func runVerifier(submissionID string) *Result {
	result := &Result{SubmissionID: submissionID}

	verifierPath := filepath.Join(taskMount, "verifier.py")
	if _, err := os.Stat(verifierPath); err != nil {
		result.Stderr = fmt.Sprintf("verifier.py not found at %s: %v", verifierPath, err)
		result.ExitCode = 127
		return result
	}

	start := time.Now()

	cmd := exec.Command(
		"python3", verifierPath,
		"--output", filepath.Join(taskMount, "output.txt"),
		"--submission-id", submissionID,
	)

	// New process group so we can kill the whole tree at once
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL, // child dies if parent dies
	}

	// Set resource limits using the newer interface
	if err := setResourceLimits(); err != nil {
		logf("failed to set resource limits: %v", err)
	}

	var stdoutBuf, stderrBuf cappedBuffer
	stdoutBuf.limit = maxOutputBytes
	stderrBuf.limit = maxOutputBytes
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Wall-clock timeout kills the process group if Python hangs
	const wallTimeout = 25 * time.Second
	timer := time.AfterFunc(wallTimeout, func() {
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			result.TimedOut = true
		}
	})
	defer timer.Stop()

	runErr := cmd.Run()

	result.ElapsedMs = time.Since(start).Milliseconds()
	result.Stdout = stdoutBuf.String()
	result.Stderr = stderrBuf.String()

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

	result.Score = parseScore(result.Stdout)
	return result
}

// parseScore extracts "SCORE: <float>" from the verifier's stdout.
// Convention: verifier.py must print this as its final output line.
func parseScore(stdout string) float64 {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if after, ok := strings.CutPrefix(line, "SCORE:"); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(after), 64); err == nil {
				return clamp(f, 0.0, 1.0)
			}
		}
	}
	return 0.0
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ── Utilities ─────────────────────────────────────────────────────────────────

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

func logf(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[guest-agent %s] "+format+"\n", append([]any{ts}, args...)...)
}

func poweroff(code int) {
	logf("powering off (exit code %d)", code)
	// LINUX_REBOOT_CMD_POWER_OFF = 0x4321FEDC
	// Use raw syscall since Go's syscall.Reboot may not be available
	_, _, errno := syscall.Syscall(syscall.SYS_REBOOT, 0, 0, 0x4321FEDC)
	if errno != 0 {
		logf("reboot syscall failed: %v", errno)
	}
	os.Exit(code) // fallback if reboot fails
}
