package vsock

import (
	"fmt"
	"net"
	"time"
)

// SignalReady connects to the host via vsock and writes "READY\n".
// The host's WaitForReady() is blocking on this message.
func SignalReady() error {
	var (
		conn net.Conn
		err  error
	)
	// Retry — host listener may not be ready yet when the VM boots
	for range 20 {
		conn, err = Dial(VMAddrCIDHost, ReadyPort)
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
