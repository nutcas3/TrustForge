package guestagent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	TaskDevice = "/dev/vdb"
	TaskMount  = "/task"
)

// MountTaskDisk mounts the task disk read-only
func MountTaskDisk() error {
	if err := os.MkdirAll(TaskMount, 0755); err != nil {
		return err
	}
	// Wait up to 1s for the kernel to expose /dev/vdb
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(TaskDevice); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(TaskDevice); err != nil {
		return fmt.Errorf("%s not present after waiting", TaskDevice)
	}
	out, err := exec.Command("mount", "-t", "ext4", "-o", "ro", TaskDevice, TaskMount).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
