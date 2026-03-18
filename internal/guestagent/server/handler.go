package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/nutcas3/trustforge/internal/guestagent/executor"
	"github.com/nutcas3/trustforge/internal/guestagent/vsock"
)

// HandleConn processes a single vsock connection
func HandleConn(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(90 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return fmt.Errorf("connection closed before command received")
	}

	var cmd Command
	if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
		return fmt.Errorf("invalid command JSON: %w", err)
	}

	// Validate input to prevent malicious content
	if err := ValidateCommand(&cmd); err != nil {
		return err
	}

	logf("executing command: type=%s submission=%s", cmd.Type, cmd.SubmissionID)

	switch cmd.Type {
	case "RUN":
		result := executor.RunVerifier(cmd.SubmissionID)
		out, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshaling result: %w", err)
		}
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

// ServeOne accepts a single command from the host, processes it, and returns.
func ServeOne() error {
	ln, err := vsock.Listen(vsock.CommandPort)
	if err != nil {
		return fmt.Errorf("listening on command port %d: %w", vsock.CommandPort, err)
	}
	defer ln.Close()

	logf("listening for commands on vsock port %d", vsock.CommandPort)

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

		// Check rate limiting before processing connection
		if !CheckRateLimit() {
			logf("connection rate limited - too many connections")
			a.conn.Close()
			return fmt.Errorf("rate limited: too many connections per minute")
		}

		defer a.conn.Close()
		return HandleConn(a.conn)
	case <-time.After(120 * time.Second):
		return fmt.Errorf("timed out waiting for host command")
	}
}

// logf is a simple logging function (will be replaced with proper logger)
func logf(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[guest-agent %s] "+format+"\n", append([]any{ts}, args...)...)
}
