//go:build linux
// +build linux

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/nutcas3/trustforge/internal/guestagent"
	"github.com/nutcas3/trustforge/internal/guestagent/server"
	"github.com/nutcas3/trustforge/internal/guestagent/vsock"
)

func main() {
	guestagent.Logf("guest-agent starting (pid %d)", os.Getpid())

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigChan
		guestagent.Logf("received signal %v, shutting down gracefully", sig)
		guestagent.Poweroff(0)
	}()

	// Mount task disk — may fail during snapshot warm-up (no vdb present)
	if err := guestagent.MountTaskDisk(); err != nil {
		guestagent.Logf("note: task disk not mounted (%v) — likely snapshot warm-up", err)
	} else {
		guestagent.Logf("task disk mounted at %s", guestagent.TaskMount)
	}

	// Signal host we are ready (triggers snapshot during warm-up)
	if err := vsock.SignalReady(); err != nil {
		guestagent.Logf("warning: could not signal ready: %v", err)
	}

	// Accept and serve exactly one RUN command then poweroff
	if err := server.ServeOne(); err != nil {
		guestagent.Logf("fatal: %v", err)
		guestagent.Poweroff(1)
	}

	guestagent.Poweroff(0)
}
