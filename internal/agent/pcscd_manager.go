/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/go-logr/logr"
)

// PCSCDManager manages the PC/SC Smart Card Daemon (pcscd) lifecycle.
// It starts pcscd in foreground mode and handles graceful shutdown.
type PCSCDManager struct {
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc
	logger logr.Logger
}

// NewPCSCDManager creates a new PCSCD manager instance.
func NewPCSCDManager(logger logr.Logger) *PCSCDManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &PCSCDManager{
		ctx:    ctx,
		cancel: cancel,
		logger: logger.WithName("pcscd-manager"),
	}
}

// Start initializes and starts the pcscd daemon.
// It runs pcscd in foreground mode with debug output and polkit disabled.
// Blocks until pcscd is ready or times out after 5 seconds.
func (p *PCSCDManager) Start() error {
	if p.cmd != nil {
		return fmt.Errorf("pcscd is already running")
	}

	p.logger.Info("Starting pcscd daemon")

	// Start pcscd with:
	// -f: foreground mode (don't daemonize)
	// --disable-polkit: disable PolicyKit (no D-Bus in container)
	// Note: Removed -d and -a debug flags for production - add back if needed
	p.cmd = exec.CommandContext(p.ctx, "/usr/sbin/pcscd", "-f", "--disable-polkit")

	// Pipe output to parent process for centralized logging
	p.cmd.Stdout = os.Stdout
	p.cmd.Stderr = os.Stderr

	// Set process group for proper signal handling
	// This ensures child processes are also signaled on shutdown
	p.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Start the process
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start pcscd: %w", err)
	}

	p.logger.Info("pcscd process started", "pid", p.cmd.Process.Pid)

	// Wait for pcscd to be ready
	if err := p.waitForReady(); err != nil {
		// If pcscd fails to start, clean up the process
		if stopErr := p.Stop(); stopErr != nil {
			p.logger.Error(stopErr, "Failed to stop pcscd during cleanup")
		}
		return fmt.Errorf("pcscd failed to become ready: %w", err)
	}

	p.logger.Info("pcscd is ready")
	return nil
}

// Stop gracefully shuts down the pcscd daemon.
// It sends SIGTERM first, then SIGKILL if the process doesn't exit within 5 seconds.
func (p *PCSCDManager) Stop() error {
	// Always cancel the context first, even if process isn't running
	p.cancel()

	if p.cmd == nil || p.cmd.Process == nil {
		p.logger.V(1).Info("pcscd is not running, nothing to stop")
		return nil
	}

	p.logger.Info("Stopping pcscd daemon", "pid", p.cmd.Process.Pid)

	// Send SIGTERM for graceful shutdown
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.logger.Error(err, "Failed to send SIGTERM to pcscd, forcing kill")
		return p.cmd.Process.Kill()
	}

	// Wait for process to exit with timeout
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			p.logger.V(1).Info("pcscd exited with error", "error", err)
		} else {
			p.logger.Info("pcscd stopped gracefully")
		}
		return err
	case <-time.After(5 * time.Second):
		// Timeout - force kill
		p.logger.Info("pcscd did not exit within timeout, forcing kill")
		if err := p.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill pcscd: %w", err)
		}
		_ = p.cmd.Wait() // Ignore wait error after force kill
		return fmt.Errorf("pcscd was forcefully killed after timeout")
	}
}

// waitForReady polls for pcscd readiness by checking if the socket exists.
// PC/SC Lite creates a socket at /var/run/pcscd/pcscd.comm when ready.
// Waits up to 5 seconds with 100ms polling interval.
func (p *PCSCDManager) waitForReady() error {
	const (
		maxAttempts  = 50                     // 50 attempts
		pollInterval = 100 * time.Millisecond // 100ms interval
		socketPath   = "/var/run/pcscd/pcscd.comm"
	)

	p.logger.V(1).Info("Waiting for pcscd to be ready", "socket", socketPath)

	for i := 0; i < maxAttempts; i++ {
		// Check if the socket exists
		if _, err := os.Stat(socketPath); err == nil {
			p.logger.V(1).Info("pcscd socket detected", "attempts", i+1)
			// Give it a tiny bit more time to fully initialize
			time.Sleep(100 * time.Millisecond)
			return nil
		}

		// Check if process is still running
		// If the process exited, no point in waiting
		if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
			return fmt.Errorf("pcscd process exited unexpectedly")
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("pcscd did not become ready within %v", time.Duration(maxAttempts)*pollInterval)
}
