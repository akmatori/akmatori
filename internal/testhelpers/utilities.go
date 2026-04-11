// Package testhelpers provides test utilities for Akmatori
package testhelpers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ========================================
// JSON Assertion Helpers
// ========================================

// AssertJSONEqual compares two JSON strings for equality (ignoring formatting)
func AssertJSONEqual(t *testing.T, expected, actual string, msg string) {
	t.Helper()

	var expectedObj, actualObj interface{}

	if err := json.Unmarshal([]byte(expected), &expectedObj); err != nil {
		t.Fatalf("%s: failed to parse expected JSON: %v", msg, err)
	}

	if err := json.Unmarshal([]byte(actual), &actualObj); err != nil {
		t.Fatalf("%s: failed to parse actual JSON: %v", msg, err)
	}

	expectedBytes, _ := json.Marshal(expectedObj)
	actualBytes, _ := json.Marshal(actualObj)

	if string(expectedBytes) != string(actualBytes) {
		t.Errorf("%s: JSON mismatch\nexpected: %s\nactual: %s", msg, expected, actual)
	}
}

// AssertJSONContainsKey checks if a JSON object contains a specific key
func AssertJSONContainsKey(t *testing.T, jsonStr string, key string, msg string) {
	t.Helper()

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		t.Fatalf("%s: failed to parse JSON: %v", msg, err)
	}

	if _, exists := obj[key]; !exists {
		t.Errorf("%s: JSON does not contain key %q", msg, key)
	}
}

// AssertJSONKeyValue checks if a JSON object has a specific key-value pair
func AssertJSONKeyValue(t *testing.T, jsonStr string, key string, expectedValue interface{}, msg string) {
	t.Helper()

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		t.Fatalf("%s: failed to parse JSON: %v", msg, err)
	}

	actualValue, exists := obj[key]
	if !exists {
		t.Errorf("%s: JSON does not contain key %q", msg, key)
		return
	}

	// Convert both to JSON for comparison to handle type differences
	expectedJSON, _ := json.Marshal(expectedValue)
	actualJSON, _ := json.Marshal(actualValue)

	if string(expectedJSON) != string(actualJSON) {
		t.Errorf("%s: JSON key %q mismatch\nexpected: %v\nactual: %v", msg, key, expectedValue, actualValue)
	}
}

// AssertJSONArrayLength checks the length of a JSON array
func AssertJSONArrayLength(t *testing.T, jsonStr string, expectedLen int, msg string) {
	t.Helper()

	var arr []interface{}
	if err := json.Unmarshal([]byte(jsonStr), &arr); err != nil {
		t.Fatalf("%s: failed to parse JSON array: %v", msg, err)
	}

	if len(arr) != expectedLen {
		t.Errorf("%s: expected array length %d, got %d", msg, expectedLen, len(arr))
	}
}

// ========================================
// Test Directory Utilities
// ========================================

// TempTestDir creates a temporary directory for tests and returns a cleanup function
func TempTestDir(t *testing.T, prefix string) (string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cleanup := sync.OnceFunc(func() {
		_ = os.RemoveAll(dir)
	})
	t.Cleanup(cleanup)

	return dir, cleanup
}

// WriteTestFile creates a test file with the given content
func WriteTestFile(t *testing.T, dir, filename, content string) string {
	t.Helper()

	path := filepath.Join(dir, filename)

	// Create parent directories if needed
	parentDir := filepath.Dir(path)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("failed to create parent directories for %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file %s: %v", path, err)
	}

	return path
}

// ReadTestFile reads a test file's content
func ReadTestFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read test file %s: %v", path, err)
	}

	return string(data)
}

// TestFileExists checks if a file exists
func TestFileExists(t *testing.T, path string) bool {
	t.Helper()

	_, err := os.Stat(path)
	return err == nil
}

// AssertFileExists fails the test if the file does not exist
func AssertFileExists(t *testing.T, path string, msg string) {
	t.Helper()

	if !TestFileExists(t, path) {
		t.Errorf("%s: file does not exist: %s", msg, path)
	}
}

// AssertFileContains fails the test if the file does not contain the substring
func AssertFileContains(t *testing.T, path, substr, msg string) {
	t.Helper()

	content := ReadTestFile(t, path)
	if !strings.Contains(content, substr) {
		t.Errorf("%s: file %s does not contain %q", msg, path, substr)
	}
}

// ========================================
// Concurrent Testing Helpers
// ========================================

// ConcurrentTest runs a function concurrently multiple times and waits for completion
func ConcurrentTest(t *testing.T, goroutines int, fn func(workerID int)) {
	t.Helper()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			fn(id)
		}(i)
	}

	wg.Wait()
}

// ConcurrentTestWithTimeout runs a function concurrently and fails if it doesn't complete in time
func ConcurrentTestWithTimeout(t *testing.T, timeout time.Duration, goroutines int, fn func(workerID int)) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		ConcurrentTest(t, goroutines, fn)
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-done:
		return
	case <-timer.C:
		t.Fatalf("concurrent test did not complete within %v", timeout)
	}
}

// ========================================
// String Helpers
// ========================================

// AssertStringPrefix checks if a string starts with a prefix
func AssertStringPrefix(t *testing.T, s, prefix string, msg string) {
	t.Helper()

	if !strings.HasPrefix(s, prefix) {
		t.Errorf("%s: expected string to start with %q, got %q", msg, prefix, s)
	}
}

// AssertStringSuffix checks if a string ends with a suffix
func AssertStringSuffix(t *testing.T, s, suffix string, msg string) {
	t.Helper()

	if !strings.HasSuffix(s, suffix) {
		t.Errorf("%s: expected string to end with %q, got %q", msg, suffix, s)
	}
}

// AssertStringLen checks if a string has a specific length
func AssertStringLen(t *testing.T, s string, expectedLen int, msg string) {
	t.Helper()

	if len(s) != expectedLen {
		t.Errorf("%s: expected string length %d, got %d", msg, expectedLen, len(s))
	}
}

// AssertStringNotEmpty checks if a string is not empty
func AssertStringNotEmpty(t *testing.T, s string, msg string) {
	t.Helper()

	if s == "" {
		t.Errorf("%s: expected non-empty string", msg)
	}
}

// ========================================
// Slice Helpers
// ========================================

// AssertSliceLen checks if a slice has a specific length
func AssertSliceLen[T any](t *testing.T, slice []T, expectedLen int, msg string) {
	t.Helper()

	if len(slice) != expectedLen {
		t.Errorf("%s: expected slice length %d, got %d", msg, expectedLen, len(slice))
	}
}

// AssertSliceContains checks if a slice contains a specific element
func AssertSliceContains[T comparable](t *testing.T, slice []T, elem T, msg string) {
	t.Helper()

	for _, e := range slice {
		if e == elem {
			return
		}
	}
	t.Errorf("%s: slice does not contain %v", msg, elem)
}

// AssertSliceNotContains checks if a slice does not contain a specific element
func AssertSliceNotContains[T comparable](t *testing.T, slice []T, elem T, msg string) {
	t.Helper()

	for _, e := range slice {
		if e == elem {
			t.Errorf("%s: slice contains %v but should not", msg, elem)
			return
		}
	}
}

// ========================================
// Map Helpers
// ========================================

// AssertMapLen checks if a map has a specific length
func AssertMapLen[K comparable, V any](t *testing.T, m map[K]V, expectedLen int, msg string) {
	t.Helper()

	if len(m) != expectedLen {
		t.Errorf("%s: expected map length %d, got %d", msg, expectedLen, len(m))
	}
}

// AssertMapContainsKey checks if a map contains a specific key
func AssertMapContainsKey[K comparable, V any](t *testing.T, m map[K]V, key K, msg string) {
	t.Helper()

	if _, exists := m[key]; !exists {
		t.Errorf("%s: map does not contain key %v", msg, key)
	}
}

// AssertMapKeyValue checks if a map has a specific key-value pair
func AssertMapKeyValue[K, V comparable](t *testing.T, m map[K]V, key K, expectedValue V, msg string) {
	t.Helper()

	actualValue, exists := m[key]
	if !exists {
		t.Errorf("%s: map does not contain key %v", msg, key)
		return
	}

	if actualValue != expectedValue {
		t.Errorf("%s: map[%v] = %v, expected %v", msg, key, actualValue, expectedValue)
	}
}

// ========================================
// Time Helpers
// ========================================

// AssertTimeAfter checks if a time is after another time
func AssertTimeAfter(t *testing.T, actual, reference time.Time, msg string) {
	t.Helper()

	if !actual.After(reference) {
		t.Errorf("%s: expected time %v to be after %v", msg, actual, reference)
	}
}

// AssertTimeBefore checks if a time is before another time
func AssertTimeBefore(t *testing.T, actual, reference time.Time, msg string) {
	t.Helper()

	if !actual.Before(reference) {
		t.Errorf("%s: expected time %v to be before %v", msg, actual, reference)
	}
}

// AssertTimeWithin checks if a time is within a duration of another time
func AssertTimeWithin(t *testing.T, actual, reference time.Time, tolerance time.Duration, msg string) {
	t.Helper()

	diff := actual.Sub(reference)
	if diff < 0 {
		diff = -diff
	}

	if diff > tolerance {
		t.Errorf("%s: time difference %v exceeds tolerance %v (actual: %v, reference: %v)",
			msg, diff, tolerance, actual, reference)
	}
}

// ========================================
// Boolean Helpers
// ========================================

// AssertTrue fails the test if the condition is false
func AssertTrue(t *testing.T, condition bool, msg string) {
	t.Helper()

	if !condition {
		t.Errorf("%s: expected true, got false", msg)
	}
}

// AssertFalse fails the test if the condition is true
func AssertFalse(t *testing.T, condition bool, msg string) {
	t.Helper()

	if condition {
		t.Errorf("%s: expected false, got true", msg)
	}
}

// ========================================
// Error Helpers
// ========================================

// AssertErrorContains checks that an error occurred and contains the given substring
func AssertErrorContains(t *testing.T, err error, substr string, msg string) {
	t.Helper()

	if err == nil {
		t.Errorf("%s: expected error containing %q, got nil", msg, substr)
		return
	}

	if !strings.Contains(err.Error(), substr) {
		t.Errorf("%s: expected error to contain %q, got %q", msg, substr, err.Error())
	}
}

// AssertPanics checks that the given function panics
func AssertPanics(t *testing.T, fn func(), msg string) {
	t.Helper()

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("%s: expected panic, but function did not panic", msg)
		}
	}()

	fn()
}

// AssertNoPanic checks that the given function does not panic
func AssertNoPanic(t *testing.T, fn func(), msg string) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: unexpected panic: %v", msg, r)
		}
	}()

	fn()
}

// ========================================
// Retry Helpers
// ========================================

// RetryUntil retries a function until it returns true or timeout is reached
// Useful for testing async operations or eventual consistency
func RetryUntil(t *testing.T, timeout time.Duration, interval time.Duration, fn func() bool, msg string) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

// AssertEventually checks that a condition becomes true within the timeout
func AssertEventually(t *testing.T, timeout time.Duration, interval time.Duration, condition func() bool, msg string) {
	t.Helper()

	if !RetryUntil(t, timeout, interval, condition, msg) {
		t.Errorf("%s: condition did not become true within %v", msg, timeout)
	}
}

// ========================================
// Environment Helpers
// ========================================

// WithEnv temporarily sets an environment variable and returns a cleanup function
func WithEnv(t *testing.T, key, value string) func() {
	t.Helper()

	original, existed := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("failed to set env var %s: %v", key, err)
	}

	cleanup := sync.OnceFunc(func() {
		if existed {
			_ = os.Setenv(key, original)
		} else {
			_ = os.Unsetenv(key)
		}
	})
	t.Cleanup(cleanup)

	return cleanup
}

// WithEnvs temporarily sets multiple environment variables
func WithEnvs(t *testing.T, envs map[string]string) func() {
	t.Helper()

	cleanups := make([]func(), 0, len(envs))
	for k, v := range envs {
		cleanups = append(cleanups, WithEnv(t, k, v))
	}

	return func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}
}

// ========================================
// HTTP Response Helpers
// ========================================

// AssertStatusCode checks the HTTP response status code
func AssertStatusCode(t *testing.T, got, expected int, msg string) {
	t.Helper()

	if got != expected {
		t.Errorf("%s: expected status %d, got %d", msg, expected, got)
	}
}

// AssertContentType checks the HTTP Content-Type header
func AssertContentType(t *testing.T, contentType, expected string, msg string) {
	t.Helper()

	// Handle Content-Type with charset (e.g., "application/json; charset=utf-8")
	if !strings.HasPrefix(contentType, expected) {
		t.Errorf("%s: expected Content-Type starting with %q, got %q", msg, expected, contentType)
	}
}

// ========================================
// Comparison Helpers
// ========================================

// AssertDeepEqual compares two values using JSON serialization
// This is useful for comparing structs with complex nested types
func AssertDeepEqual(t *testing.T, expected, actual interface{}, msg string) {
	t.Helper()

	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("%s: failed to marshal expected value: %v", msg, err)
	}

	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatalf("%s: failed to marshal actual value: %v", msg, err)
	}

	if string(expectedJSON) != string(actualJSON) {
		t.Errorf("%s: values differ\nexpected: %s\nactual: %s", msg, expectedJSON, actualJSON)
	}
}

// ========================================
// Counter Helper for Testing
// ========================================

// CallCounter is a thread-safe counter for tracking function calls in tests
type CallCounter struct {
	count int64
}

// NewCallCounter creates a new CallCounter
func NewCallCounter() *CallCounter {
	return &CallCounter{}
}

// Inc increments the counter
func (c *CallCounter) Inc() {
	atomic.AddInt64(&c.count, 1)
}

// Count returns the current count
func (c *CallCounter) Count() int64 {
	return atomic.LoadInt64(&c.count)
}

// Reset resets the counter to zero
func (c *CallCounter) Reset() {
	atomic.StoreInt64(&c.count, 0)
}

// AssertCount checks the counter value
func (c *CallCounter) AssertCount(t *testing.T, expected int64, msg string) {
	t.Helper()

	actual := c.Count()
	if actual != expected {
		t.Errorf("%s: expected call count %d, got %d", msg, expected, actual)
	}
}
