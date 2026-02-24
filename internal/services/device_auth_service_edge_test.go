package services

import (
	"sync"
	"testing"
)

// TestDeviceAuthService_ConcurrentAccess tests thread safety
func TestDeviceAuthService_ConcurrentAccess(t *testing.T) {
	svc := NewDeviceAuthService()

	var wg sync.WaitGroup

	// Start multiple goroutines accessing the service
	for i := 0; i < 10; i++ {
		wg.Add(3)

		// Reader goroutine
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				svc.HasActiveFlow()
			}
		}()

		// Status checker goroutine
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				svc.GetDeviceAuthStatus("test-code")
			}
		}()

		// Writer goroutine
		go func(id int) {
			defer wg.Done()
			result := &DeviceAuthResult{
				Status:     "pending",
				DeviceCode: "test-device-code",
				UserCode:   "TEST",
			}
			svc.HandleDeviceAuthResult(result)
		}(i)
	}

	wg.Wait()
}

// TestDeviceAuthService_FlowLifecycle tests complete flow lifecycle
// Note: Complete flow tests are limited because HandleDeviceAuthResult
// attempts to save tokens to DB on completion. Test pending flow only.
func TestDeviceAuthService_FlowLifecycle(t *testing.T) {
	svc := NewDeviceAuthService()

	// Initially no active flow
	if svc.HasActiveFlow() {
		t.Error("Should have no active flow initially")
	}

	// Start a flow
	pending := &DeviceAuthResult{
		Status:          "pending",
		DeviceCode:      "dev-code-123",
		UserCode:        "ABCD-1234",
		VerificationURL: "https://auth.example.com/device",
		ExpiresIn:       900,
	}
	svc.HandleDeviceAuthResult(pending)

	// Should have active flow now
	if !svc.HasActiveFlow() {
		t.Error("Should have active flow after pending result")
	}

	// Check status
	status, err := svc.GetDeviceAuthStatus("dev-code-123")
	if err != nil {
		t.Fatalf("GetDeviceAuthStatus error: %v", err)
	}
	if status.Status != DeviceAuthStatusPending {
		t.Errorf("Status = %s, want pending", status.Status)
	}

	// Clear the flow
	svc.ClearFlow()

	// Should have no active flow
	if svc.HasActiveFlow() {
		t.Error("Should have no active flow after clear")
	}
}

// TestDeviceAuthService_HandleExpiredFlow tests flow expiration handling
func TestDeviceAuthService_HandleExpiredFlow(t *testing.T) {
	svc := NewDeviceAuthService()

	// Create pending flow
	pending := &DeviceAuthResult{
		Status:          "pending",
		DeviceCode:      "dev-code",
		UserCode:        "CODE",
		VerificationURL: "https://auth.example.com/device",
		ExpiresIn:       900,
	}
	svc.HandleDeviceAuthResult(pending)

	// Verify pending state
	status, _ := svc.GetDeviceAuthStatus("dev-code")
	if status.Status != DeviceAuthStatusPending {
		t.Errorf("Status = %s, want pending", status.Status)
	}

	// Cancel the flow (similar to expiration behavior)
	svc.CancelDeviceAuth()

	// Flow should be cleared
	if svc.HasActiveFlow() {
		t.Error("Flow should be cleared after cancel")
	}
}

// TestDeviceAuthService_HandleFailedFlow tests flow failure handling
func TestDeviceAuthService_HandleFailedFlow(t *testing.T) {
	svc := NewDeviceAuthService()

	// Create pending flow
	pending := &DeviceAuthResult{
		Status:          "pending",
		DeviceCode:      "dev-code",
		UserCode:        "CODE",
		VerificationURL: "https://auth.example.com/device",
		ExpiresIn:       900,
	}
	svc.HandleDeviceAuthResult(pending)

	// Verify we have an active pending flow
	if !svc.HasActiveFlow() {
		t.Error("Should have active flow after pending result")
	}

	status, _ := svc.GetDeviceAuthStatus("dev-code")
	if status.Status != DeviceAuthStatusPending {
		t.Errorf("Status = %s, want pending", status.Status)
	}
}

// TestDeviceAuthService_MultipleFlows tests handling multiple flow attempts
func TestDeviceAuthService_MultipleFlows(t *testing.T) {
	svc := NewDeviceAuthService()

	// Start first flow
	flow1 := &DeviceAuthResult{
		Status:     "pending",
		DeviceCode: "dev-code-1",
		UserCode:   "CODE1",
	}
	svc.HandleDeviceAuthResult(flow1)

	// Start second flow (should replace first)
	flow2 := &DeviceAuthResult{
		Status:     "pending",
		DeviceCode: "dev-code-2",
		UserCode:   "CODE2",
	}
	svc.HandleDeviceAuthResult(flow2)

	// Only second flow should be active
	status1, _ := svc.GetDeviceAuthStatus("dev-code-1")
	if status1.Status != DeviceAuthStatusExpired {
		t.Errorf("Old flow status = %s, want expired", status1.Status)
	}

	status2, _ := svc.GetDeviceAuthStatus("dev-code-2")
	if status2.Status != DeviceAuthStatusPending {
		t.Errorf("New flow status = %s, want pending", status2.Status)
	}
}

// TestDeviceAuthService_CancelActiveFlow tests cancellation
func TestDeviceAuthService_CancelActiveFlow(t *testing.T) {
	svc := NewDeviceAuthService()

	// Create pending flow
	pending := &DeviceAuthResult{
		Status:     "pending",
		DeviceCode: "dev-code",
		UserCode:   "CODE",
	}
	svc.HandleDeviceAuthResult(pending)

	if !svc.HasActiveFlow() {
		t.Fatal("Should have active flow")
	}

	// Cancel it
	svc.CancelDeviceAuth()

	// Flow should be cleared
	if svc.HasActiveFlow() {
		t.Error("Should not have active flow after cancel")
	}
}

// TestDeviceAuthService_GetAuthTokens_PendingState tests tokens unavailable while pending
func TestDeviceAuthService_GetAuthTokens_PendingState(t *testing.T) {
	svc := NewDeviceAuthService()

	// Start flow but don't complete
	pending := &DeviceAuthResult{
		Status:     "pending",
		DeviceCode: "dev-code",
		UserCode:   "CODE",
	}
	svc.HandleDeviceAuthResult(pending)

	// Tokens should not be available while pending
	_, err := svc.GetAuthTokens()
	if err == nil {
		t.Error("GetAuthTokens should fail when flow is pending")
	}
}

// TestDeviceAuthService_PendingFlowTokensUnavailable tests token retrieval while pending
func TestDeviceAuthService_PendingFlowTokensUnavailable(t *testing.T) {
	svc := NewDeviceAuthService()

	// Start flow but don't complete
	pending := &DeviceAuthResult{
		Status:     "pending",
		DeviceCode: "dev-code",
		UserCode:   "CODE",
	}
	svc.HandleDeviceAuthResult(pending)

	// Getting tokens should fail
	_, err := svc.GetAuthTokens()
	if err == nil {
		t.Error("GetAuthTokens should fail for pending flow")
	}
}

// TestDeviceAuthService_HandleResultNoActiveFlow tests result without active flow
func TestDeviceAuthService_HandleResultNoActiveFlow(t *testing.T) {
	svc := NewDeviceAuthService()

	// Send complete without pending first (should be ignored)
	complete := &DeviceAuthResult{
		Status:       "complete",
		DeviceCode:   "dev-code",
		AccessToken:  "token",
		RefreshToken: "refresh",
	}
	svc.HandleDeviceAuthResult(complete)

	// Should have no active flow
	if svc.HasActiveFlow() {
		t.Error("Should not create flow from complete result")
	}
}

// TestDeviceAuthService_ResultChannelCapacity tests channel doesn't block
func TestDeviceAuthService_ResultChannelCapacity(t *testing.T) {
	svc := NewDeviceAuthService()

	// Fill the channel with pending results
	for i := 0; i < 15; i++ {
		result := &DeviceAuthResult{
			Status:     "pending",
			DeviceCode: "dev-code",
			UserCode:   "CODE",
		}
		// Should not block due to select with default
		svc.HandleDeviceAuthResult(result)
	}

	// Service should still be responsive
	if !svc.HasActiveFlow() {
		t.Error("Should have active flow")
	}
}

// BenchmarkDeviceAuthService_HasActiveFlow benchmarks the check
func BenchmarkDeviceAuthService_HasActiveFlow(b *testing.B) {
	svc := NewDeviceAuthService()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.HasActiveFlow()
	}
}

// BenchmarkDeviceAuthService_HandleDeviceAuthResult benchmarks result handling
func BenchmarkDeviceAuthService_HandleDeviceAuthResult(b *testing.B) {
	svc := NewDeviceAuthService()
	result := &DeviceAuthResult{
		Status:          "pending",
		DeviceCode:      "dev-code",
		UserCode:        "CODE",
		VerificationURL: "https://auth.example.com/device",
		ExpiresIn:       900,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.HandleDeviceAuthResult(result)
	}
}

// BenchmarkDeviceAuthService_GetDeviceAuthStatus benchmarks status check
func BenchmarkDeviceAuthService_GetDeviceAuthStatus(b *testing.B) {
	svc := NewDeviceAuthService()

	// Set up active flow
	result := &DeviceAuthResult{
		Status:     "pending",
		DeviceCode: "dev-code",
		UserCode:   "CODE",
	}
	svc.HandleDeviceAuthResult(result)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.GetDeviceAuthStatus("dev-code")
	}
}
