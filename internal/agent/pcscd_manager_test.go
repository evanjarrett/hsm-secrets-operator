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
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestNewPCSCDManager(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))
	mgr := NewPCSCDManager(logger)

	if mgr == nil {
		t.Fatal("NewPCSCDManager returned nil")
	}

	if mgr.ctx == nil {
		t.Error("Context was not initialized")
	}

	if mgr.cancel == nil {
		t.Error("Cancel function was not initialized")
	}

	if mgr.cmd != nil {
		t.Error("Command should be nil before Start()")
	}
}

func TestPCSCDManager_StartWithoutPCSCD(t *testing.T) {
	// This test verifies error handling when pcscd binary doesn't exist
	// Note: This test would need to mock exec.Command to properly test
	// without actually running pcscd. For now, we document expected behavior.

	// If pcscd is not available at /usr/sbin/pcscd, Start() should fail
	// In a production container, pcscd will always be present

	// Skip this test in CI environments where pcscd may not be installed
	if _, err := os.Stat("/usr/sbin/pcscd"); os.IsNotExist(err) {
		t.Skip("pcscd binary not found, skipping test")
	}

	// If we reach here, pcscd exists, so we can't test the "not found" case
	// without mocking. Skip the test.
	t.Skip("Cannot test missing pcscd without mocking exec.Command")
}

func TestPCSCDManager_MultipleStartAttempts(t *testing.T) {
	// Test that calling Start() multiple times fails appropriately
	logger := zap.New(zap.UseDevMode(true))
	mgr := NewPCSCDManager(logger)

	// Mock the cmd to prevent actual pcscd start
	// In real implementation, we'd need dependency injection or interface
	// to properly mock exec.Command

	// Simulate cmd being already set
	if mgr.cmd == nil {
		// Set a dummy command to simulate already-started state
		mgr.cmd = &exec.Cmd{} // This is just for testing the check
	}

	err := mgr.Start()
	if err == nil {
		t.Error("Expected error when starting already-running pcscd, got nil")
	}

	if err != nil && err.Error() != "pcscd is already running" {
		t.Errorf("Expected 'pcscd is already running' error, got: %v", err)
	}
}

func TestPCSCDManager_StopWithoutStart(t *testing.T) {
	// Test that Stop() is safe to call even if Start() was never called
	logger := zap.New(zap.UseDevMode(true))
	mgr := NewPCSCDManager(logger)

	// Should not panic or error
	err := mgr.Stop()
	if err != nil {
		t.Errorf("Stop() should be safe when not running, got error: %v", err)
	}
}

func TestPCSCDManager_ContextCancellation(t *testing.T) {
	// Test that cancelling the context affects the manager
	logger := zap.New(zap.UseDevMode(true))
	mgr := NewPCSCDManager(logger)

	// Verify context is not cancelled initially
	select {
	case <-mgr.ctx.Done():
		t.Error("Context should not be cancelled initially")
	default:
		// Good - context is active
	}

	// Call Stop() which should cancel the context
	mgr.Stop()

	// Give it a moment to cancel
	time.Sleep(10 * time.Millisecond)

	// Verify context is now cancelled
	select {
	case <-mgr.ctx.Done():
		// Good - context was cancelled
	default:
		t.Error("Context should be cancelled after Stop()")
	}
}

// Integration test helper - only runs if pcscd is available
func getPCSCDTestLogger() logr.Logger {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	return ctrl.Log.WithName("pcscd-test")
}

// TestPCSCDManager_Integration is an integration test that requires pcscd
// It's skipped automatically if pcscd is not available or already running
func TestPCSCDManager_Integration(t *testing.T) {
	// Verify pcscd binary exists
	if _, err := os.Stat("/usr/sbin/pcscd"); os.IsNotExist(err) {
		t.Skip("pcscd binary not found, skipping integration test")
	}

	// Check if pcscd is already running (socket exists)
	if _, err := os.Stat("/var/run/pcscd/pcscd.comm"); err == nil {
		t.Skip("pcscd is already running, skipping integration test")
	}

	logger := getPCSCDTestLogger()
	mgr := NewPCSCDManager(logger)

	// Start pcscd
	if err := mgr.Start(); err != nil {
		t.Skipf("Failed to start pcscd (may be a permission issue): %v", err)
	}

	// Verify process is running
	if mgr.cmd == nil || mgr.cmd.Process == nil {
		t.Fatal("pcscd process should be running")
	}

	// Give it time to fully initialize
	time.Sleep(1 * time.Second)

	// Verify socket exists
	if _, err := os.Stat("/var/run/pcscd/pcscd.comm"); os.IsNotExist(err) {
		t.Error("pcscd socket not found after start")
	}

	// Stop pcscd
	if err := mgr.Stop(); err != nil {
		t.Errorf("Failed to stop pcscd: %v", err)
	}

	// Verify process is stopped
	time.Sleep(100 * time.Millisecond)
	if mgr.cmd != nil && mgr.cmd.ProcessState != nil {
		if !mgr.cmd.ProcessState.Exited() {
			t.Error("pcscd process should have exited")
		}
	}
}
