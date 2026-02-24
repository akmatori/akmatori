package slack

import (
	"sync"
	"testing"
	"time"
)

// --- Manager unit tests ---

func TestNewManager(t *testing.T) {
	m := NewManager()

	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.reloadChan == nil {
		t.Error("reloadChan should be initialized")
	}
	if m.running {
		t.Error("new manager should not be running")
	}
	if m.client != nil {
		t.Error("new manager should have nil client")
	}
	if m.socketClient != nil {
		t.Error("new manager should have nil socketClient")
	}
}

func TestManager_GetClient_NilWhenNotStarted(t *testing.T) {
	m := NewManager()

	client := m.GetClient()
	if client != nil {
		t.Error("GetClient should return nil when manager is not started")
	}
}

func TestManager_GetSocketClient_NilWhenNotStarted(t *testing.T) {
	m := NewManager()

	socketClient := m.GetSocketClient()
	if socketClient != nil {
		t.Error("GetSocketClient should return nil when manager is not started")
	}
}

func TestManager_IsRunning_FalseWhenNotStarted(t *testing.T) {
	m := NewManager()

	if m.IsRunning() {
		t.Error("IsRunning should return false when manager is not started")
	}
}

func TestManager_SetEventHandler(t *testing.T) {
	m := NewManager()

	// Event handler is nil by default
	if m.eventHandler != nil {
		t.Error("eventHandler should be nil initially")
	}

	// Note: Can't easily test SetEventHandler without proper slack types
	// The function signature requires (*socketmode.Client, *slack.Client)
	// This test verifies the manager's initial state
}

func TestManager_TriggerReload_NonBlocking(t *testing.T) {
	m := NewManager()

	// Should not block even without a receiver
	done := make(chan bool, 1)
	go func() {
		m.TriggerReload()
		done <- true
	}()

	// Give goroutine time to complete
	select {
	case <-done:
		// Success - TriggerReload returned
	case <-time.After(100 * time.Millisecond):
		t.Error("TriggerReload should be non-blocking")
	}
}

func TestManager_TriggerReload_Coalescing(t *testing.T) {
	m := NewManager()

	// Multiple triggers should coalesce (buffer size is 1)
	m.TriggerReload()
	m.TriggerReload()
	m.TriggerReload()

	// Drain the channel
	select {
	case <-m.reloadChan:
		// Got one reload signal
	default:
		t.Error("expected at least one reload signal")
	}

	// Channel should now be empty (coalesced)
	select {
	case <-m.reloadChan:
		t.Error("reload signals should coalesce, got more than one")
	default:
		// Expected - channel is empty
	}
}

func TestManager_Stop_NoopWhenNotRunning(t *testing.T) {
	m := NewManager()

	// Should not panic or error when stopping a non-running manager
	m.Stop()

	if m.IsRunning() {
		t.Error("manager should still not be running after Stop")
	}
}

func TestManager_ConcurrentGettersAreSafe(t *testing.T) {
	m := NewManager()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = m.GetClient()
		}()
		go func() {
			defer wg.Done()
			_ = m.GetSocketClient()
		}()
		go func() {
			defer wg.Done()
			_ = m.IsRunning()
		}()
	}
	wg.Wait()
	// No panic = success (testing concurrent safety)
}

// --- Manager state transitions ---

func TestManager_StateAfterStop(t *testing.T) {
	m := NewManager()

	// Simulate having been started (set internal state directly for unit test)
	m.mu.Lock()
	m.running = true
	m.stopChan = make(chan struct{})
	m.doneChan = make(chan struct{})
	close(m.doneChan) // Simulate socket mode finished
	m.mu.Unlock()

	// Stop should reset state
	m.Stop()

	if m.IsRunning() {
		t.Error("IsRunning should be false after Stop")
	}
	if m.GetClient() != nil {
		t.Error("GetClient should return nil after Stop")
	}
	if m.GetSocketClient() != nil {
		t.Error("GetSocketClient should return nil after Stop")
	}
}

// --- State consistency tests ---

func TestManager_StateConsistency(t *testing.T) {
	m := NewManager()

	// Initial state should be consistent
	if m.IsRunning() {
		t.Error("new manager should not be running")
	}
	if m.GetClient() != nil {
		t.Error("new manager should have nil client")
	}
	if m.GetSocketClient() != nil {
		t.Error("new manager should have nil socket client")
	}

	// State should remain consistent after Stop on non-running manager
	m.Stop()

	if m.IsRunning() {
		t.Error("manager should not be running after Stop")
	}
	if m.GetClient() != nil {
		t.Error("manager should have nil client after Stop")
	}
}
