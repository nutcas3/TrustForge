// Package vsock handles bidirectional communication between the Go host
// and the guest agent running inside the Firecracker VM.
//
// Firecracker exposes a virtio-vsock device. The host connects to guest CID 3
// on a known port (52) to send commands and receive results.
//
// This avoids all network configuration — no TAP device, no IP assignment,
// no firewall rules. Pure hardware-boundary communication.
package vsock

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/vsock"
	tfmodels "github.com/nutcas3/trustforge/pkg/models"
	"github.com/sirupsen/logrus"
)

const (
	GuestCID = 3
	CommandPort = 52
	ReadyPort = 53
)

// Command is sent from host to guest agent
type Command struct {
	Type         string `json:"type"`
	SubmissionID string `json:"submission_id"`
}

// HostClient sends commands to a guest agent via vsock
type HostClient struct {
	vmID        string
	vsockPath   string // path to the vsock UDS on the host (Firecracker creates this)
	dialTimeout time.Duration
	logger      *logrus.Logger
}

// NewHostClient creates a new vsock client for communicating with a guest
func NewHostClient(vmID, vsockPath string, logger *logrus.Logger) *HostClient {
	return &HostClient{
		vmID:        vmID,
		vsockPath:   vsockPath,
		dialTimeout: 5 * time.Second,
		logger:      logger,
	}
}

// WaitForReady blocks until the guest agent sends its "READY" signal.
// The guest agent writes "READY\n" to the ready port when Python is initialized.
func (c *HostClient) WaitForReady(ctx context.Context) error {
	logger := c.logger.WithField("vm_id", c.vmID)
	logger.Debug("waiting for guest ready signal via vsock")

	// Firecracker exposes vsock as a Unix socket on the host side.
	// We connect to the UDS and proxy to the guest port.
	conn, err := c.dialWithRetry(ctx, ReadyPort)
	if err != nil {
		return fmt.Errorf("dialing guest ready port: %w", err)
	}
	defer conn.Close()

	// Set deadline based on context
	deadline, ok := ctx.Deadline()
	if ok {
		conn.SetDeadline(deadline)
	}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "READY" {
			logger.Debug("received READY from guest agent")
			return nil
		}
		logger.WithField("guest_msg", line).Debug("guest startup message")
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading from guest ready port: %w", err)
	}

	return fmt.Errorf("connection closed without READY signal")
}

// RunVerifier sends the RUN command to the guest agent and waits for the result.
// The guest agent executes verifier.py and streams results back.
func (c *HostClient) RunVerifier(ctx context.Context, submissionID string) (*tfmodels.EvaluationResult, error) {
	logger := c.logger.WithFields(logrus.Fields{
		"vm_id":         c.vmID,
		"submission_id": submissionID,
	})
	logger.Info("sending RUN command to guest agent")

	conn, err := c.dialWithRetry(ctx, CommandPort)
	if err != nil {
		return nil, fmt.Errorf("dialing guest command port: %w", err)
	}
	defer conn.Close()

	// Set deadline
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	// Send command as JSON line
	cmd := Command{
		Type:         "RUN",
		SubmissionID: submissionID,
	}
	cmdBytes, _ := json.Marshal(cmd)
	cmdBytes = append(cmdBytes, '\n')

	if _, err := conn.Write(cmdBytes); err != nil {
		return nil, fmt.Errorf("sending RUN command: %w", err)
	}

	// Read result JSON line
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("reading evaluation result: %w", err)
		}
		return nil, fmt.Errorf("connection closed before result received")
	}

	var result tfmodels.EvaluationResult
	if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parsing evaluation result: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"exit_code":  result.ExitCode,
		"score":      result.Score,
		"elapsed_ms": result.ElapsedMs,
	}).Info("evaluation complete")

	return &result, nil
}

// dialWithRetry dials the vsock guest port with exponential backoff.
// The guest agent may take a few milliseconds to start listening after resume.
func (c *HostClient) dialWithRetry(ctx context.Context, port uint32) (net.Conn, error) {
	backoff := 10 * time.Millisecond
	maxBackoff := 500 * time.Millisecond
	maxAttempts := 10

	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Firecracker vsock UDS proxy: connect to the host-side UDS,
		// then write "CONNECT <port>\n" to be proxied to the guest.
		conn, err := net.DialTimeout("unix", c.vsockPath, c.dialTimeout)
		if err == nil {
			// Send the CONNECT command to the vsock proxy
			fmt.Fprintf(conn, "CONNECT %d\n", port)

			// Read the OK response
			reader := bufio.NewReader(conn)
			resp, err := reader.ReadString('\n')
			if err == nil && resp == "OK 0\n" {
				return conn, nil
			}
			conn.Close()
		}

		c.logger.WithFields(logrus.Fields{
			"attempt": attempt + 1,
			"port":    port,
			"err":     err,
		}).Debug("vsock dial attempt failed, retrying")

		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	return nil, fmt.Errorf("exhausted %d vsock dial attempts for port %d", maxAttempts, port)
}

// HostListener listens for incoming connections from guest VMs on the host side.
// Used when the guest initiates contact (e.g., sending a ready signal).
type HostListener struct {
	port   uint32
	logger *logrus.Logger
}

// NewHostListener creates a vsock listener on the host
func NewHostListener(port uint32, logger *logrus.Logger) *HostListener {
	return &HostListener{port: port, logger: logger}
}

// Listen starts accepting connections from guest VMs
func (l *HostListener) Listen(ctx context.Context, handler func(conn net.Conn)) error {
	listener, err := vsock.Listen(l.port, nil)
	if err != nil {
		return fmt.Errorf("listening on vsock port %d: %w", l.port, err)
	}
	defer listener.Close()

	l.logger.WithField("port", l.port).Info("vsock host listener started")

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // context cancelled
			}
			l.logger.WithError(err).Error("vsock accept error")
			continue
		}
		go handler(conn)
	}
}
