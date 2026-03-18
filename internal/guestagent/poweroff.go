//go:build linux

package guestagent

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// Logf is a simple logging function
func Logf(format string, args ...any) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(os.Stderr, "[guest-agent %s] "+format+"\n", append([]any{ts}, args...)...)
}

// Poweroff shuts down the VM gracefully
func Poweroff(code int) {
	Logf("powering off (exit code %d)", code)
	// LINUX_REBOOT_CMD_POWER_OFF = 0x4321FEDC
	// Use raw syscall since Go's syscall.Reboot may not be available
	_, _, errno := syscall.Syscall(syscall.SYS_REBOOT, 0, 0, 0x4321FEDC)
	if errno != 0 {
		Logf("reboot syscall failed: %v", errno)
	}
	os.Exit(code) // fallback if reboot fails
}
