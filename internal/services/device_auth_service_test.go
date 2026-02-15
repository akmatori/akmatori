package services

import (
	"testing"
)

func TestNewDeviceAuthService(t *testing.T) {
	svc := NewDeviceAuthService()
	if svc == nil {
		t.Fatal("NewDeviceAuthService() returned nil")
	}
	if svc.resultChan == nil {
		t.Error("resultChan should not be nil")
	}
}

func TestDeviceAuthService_HasActiveFlow(t *testing.T) {
	svc := NewDeviceAuthService()
	if svc.HasActiveFlow() {
		t.Error("HasActiveFlow() should return false when no flow is active")
	}
}

func TestDeviceAuthService_GetDeviceAuthStatus_NoActiveFlow(t *testing.T) {
	svc := NewDeviceAuthService()
	status, err := svc.GetDeviceAuthStatus("some-code")
	if err != nil {
		t.Errorf("GetDeviceAuthStatus() error = %v", err)
	}
	if status.Status != DeviceAuthStatusExpired {
		t.Errorf("GetDeviceAuthStatus() status = %s, want %s", status.Status, DeviceAuthStatusExpired)
	}
}

func TestDeviceAuthService_CancelDeviceAuth_NoActiveFlow(t *testing.T) {
	svc := NewDeviceAuthService()
	// Should not panic when no flow is active
	svc.CancelDeviceAuth()
}

func TestDeviceAuthService_ClearFlow(t *testing.T) {
	svc := NewDeviceAuthService()
	// Should not panic when no flow is active
	svc.ClearFlow()
	if svc.HasActiveFlow() {
		t.Error("ClearFlow() should clear any active flow")
	}
}

func TestDeviceAuthService_HandleDeviceAuthResult_Pending(t *testing.T) {
	svc := NewDeviceAuthService()

	// Send a pending result
	result := &DeviceAuthResult{
		Status:          "pending",
		DeviceCode:      "test-device-code",
		UserCode:        "TEST-CODE",
		VerificationURL: "https://example.com/verify",
		ExpiresIn:       900,
	}

	svc.HandleDeviceAuthResult(result)

	if !svc.HasActiveFlow() {
		t.Error("HandleDeviceAuthResult() should create active flow for pending status")
	}

	// Verify the flow was stored correctly
	status, err := svc.GetDeviceAuthStatus("test-device-code")
	if err != nil {
		t.Errorf("GetDeviceAuthStatus() error = %v", err)
	}
	if status.Status != DeviceAuthStatusPending {
		t.Errorf("Status = %s, want %s", status.Status, DeviceAuthStatusPending)
	}
}

func TestDeviceAuthService_HandleDeviceAuthResult_DeviceCodeMismatch(t *testing.T) {
	svc := NewDeviceAuthService()

	// Send a pending result to create a flow
	result := &DeviceAuthResult{
		Status:          "pending",
		DeviceCode:      "test-device-code",
		UserCode:        "TEST-CODE",
		VerificationURL: "https://example.com/verify",
		ExpiresIn:       900,
	}
	svc.HandleDeviceAuthResult(result)

	// Check status with wrong device code
	status, err := svc.GetDeviceAuthStatus("wrong-device-code")
	if err != nil {
		t.Errorf("GetDeviceAuthStatus() error = %v", err)
	}
	if status.Status != DeviceAuthStatusExpired {
		t.Errorf("Status = %s, want %s for mismatched device code", status.Status, DeviceAuthStatusExpired)
	}
}

func TestDeviceAuthService_GetAuthTokens_NoFlow(t *testing.T) {
	svc := NewDeviceAuthService()
	_, err := svc.GetAuthTokens()
	if err == nil {
		t.Error("GetAuthTokens() should return error when no flow is active")
	}
}

func TestDeviceAuthService_HandleDeviceAuthResult_ImmediateFailure(t *testing.T) {
	svc := NewDeviceAuthService()

	// Send a failed result with no active flow (simulates immediate failure)
	result := &DeviceAuthResult{
		Status: "failed",
		Error:  "device code request failed with status 429 Too Many Requests",
	}

	svc.HandleDeviceAuthResult(result)

	// The result should be available in the channel for WaitForInitialResponse
	select {
	case received := <-svc.resultChan:
		if received.Status != "failed" {
			t.Errorf("Expected status 'failed', got %s", received.Status)
		}
		if received.Error == "" {
			t.Error("Expected error message to be present")
		}
	default:
		t.Error("Expected failed result to be sent to result channel")
	}
}

func TestDeviceAuthStatusConstants(t *testing.T) {
	if DeviceAuthStatusPending != "pending" {
		t.Error("DeviceAuthStatusPending should be 'pending'")
	}
	if DeviceAuthStatusComplete != "complete" {
		t.Error("DeviceAuthStatusComplete should be 'complete'")
	}
	if DeviceAuthStatusExpired != "expired" {
		t.Error("DeviceAuthStatusExpired should be 'expired'")
	}
	if DeviceAuthStatusFailed != "failed" {
		t.Error("DeviceAuthStatusFailed should be 'failed'")
	}
}
