package testhelpers

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// ========================================
// JSON Assertion Tests
// ========================================

func TestAssertJSONEqual_Success(t *testing.T) {
	expected := `{"name": "test", "count": 5}`
	actual := `{"count":5,"name":"test"}`

	mockT := &testing.T{}
	AssertJSONEqual(mockT, expected, actual, "JSON should be equal")

	if mockT.Failed() {
		t.Error("AssertJSONEqual should not have failed for equivalent JSON")
	}
}

func TestAssertJSONContainsKey_Success(t *testing.T) {
	jsonStr := `{"name": "test", "count": 5}`

	mockT := &testing.T{}
	AssertJSONContainsKey(mockT, jsonStr, "name", "should contain key")

	if mockT.Failed() {
		t.Error("AssertJSONContainsKey should not have failed")
	}
}

func TestAssertJSONKeyValue_Success(t *testing.T) {
	jsonStr := `{"name": "test", "count": 5}`

	mockT := &testing.T{}
	AssertJSONKeyValue(mockT, jsonStr, "name", "test", "key value check")

	if mockT.Failed() {
		t.Error("AssertJSONKeyValue should not have failed")
	}
}

func TestAssertJSONArrayLength_Success(t *testing.T) {
	jsonStr := `[1, 2, 3, 4, 5]`

	mockT := &testing.T{}
	AssertJSONArrayLength(mockT, jsonStr, 5, "array length check")

	if mockT.Failed() {
		t.Error("AssertJSONArrayLength should not have failed")
	}
}

// ========================================
// Test Directory Tests
// ========================================

func TestTempTestDir(t *testing.T) {
	dir, cleanup := TempTestDir(t, "testhelpers-")
	defer cleanup()

	// Check that the directory exists
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("temp dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("temp path should be a directory")
	}

	// After cleanup, the directory should be removed
	cleanup()
	_, err = os.Stat(dir)
	if !os.IsNotExist(err) {
		t.Error("temp dir should be removed after cleanup")
	}
}

func TestWriteTestFile(t *testing.T) {
	dir, cleanup := TempTestDir(t, "testhelpers-")
	defer cleanup()

	content := "test content"
	path := WriteTestFile(t, dir, "test.txt", content)

	// Check that the file exists
	if !TestFileExists(t, path) {
		t.Error("test file should exist")
	}

	// Check the content
	readContent := ReadTestFile(t, path)
	if readContent != content {
		t.Errorf("expected content %q, got %q", content, readContent)
	}
}

func TestWriteTestFile_Nested(t *testing.T) {
	dir, cleanup := TempTestDir(t, "testhelpers-")
	defer cleanup()

	content := "nested content"
	path := WriteTestFile(t, dir, "subdir/nested/test.txt", content)

	// Check that the file exists
	if !TestFileExists(t, path) {
		t.Error("nested test file should exist")
	}

	// Check parent directories were created
	parentDir := filepath.Dir(path)
	info, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("parent dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("parent should be a directory")
	}
}

func TestAssertFileContains(t *testing.T) {
	dir, cleanup := TempTestDir(t, "testhelpers-")
	defer cleanup()

	content := "hello world"
	path := WriteTestFile(t, dir, "test.txt", content)

	mockT := &testing.T{}
	AssertFileContains(mockT, path, "hello", "file should contain 'hello'")

	if mockT.Failed() {
		t.Error("AssertFileContains should not have failed")
	}
}

// ========================================
// Concurrent Testing Tests
// ========================================

func TestConcurrentTest(t *testing.T) {
	var counter int64 = 0

	ConcurrentTest(t, 10, func(workerID int) {
		atomic.AddInt64(&counter, 1)
	})

	if counter != 10 {
		t.Errorf("expected counter 10, got %d", counter)
	}
}

func TestConcurrentTestWithTimeout_Success(t *testing.T) {
	mockT := &testing.T{}

	ConcurrentTestWithTimeout(mockT, time.Second, 5, func(workerID int) {
		time.Sleep(10 * time.Millisecond)
	})

	if mockT.Failed() {
		t.Error("concurrent test should have completed within timeout")
	}
}

// ========================================
// String Helper Tests
// ========================================

func TestAssertStringPrefix(t *testing.T) {
	mockT := &testing.T{}
	AssertStringPrefix(mockT, "hello world", "hello", "prefix check")

	if mockT.Failed() {
		t.Error("AssertStringPrefix should not have failed")
	}
}

func TestAssertStringSuffix(t *testing.T) {
	mockT := &testing.T{}
	AssertStringSuffix(mockT, "hello world", "world", "suffix check")

	if mockT.Failed() {
		t.Error("AssertStringSuffix should not have failed")
	}
}

func TestAssertStringLen(t *testing.T) {
	mockT := &testing.T{}
	AssertStringLen(mockT, "hello", 5, "length check")

	if mockT.Failed() {
		t.Error("AssertStringLen should not have failed")
	}
}

func TestAssertStringNotEmpty(t *testing.T) {
	mockT := &testing.T{}
	AssertStringNotEmpty(mockT, "test", "not empty check")

	if mockT.Failed() {
		t.Error("AssertStringNotEmpty should not have failed")
	}
}

// ========================================
// Slice Helper Tests
// ========================================

func TestAssertSliceLen(t *testing.T) {
	mockT := &testing.T{}
	slice := []int{1, 2, 3}
	AssertSliceLen(mockT, slice, 3, "slice length check")

	if mockT.Failed() {
		t.Error("AssertSliceLen should not have failed")
	}
}

func TestAssertSliceContains(t *testing.T) {
	mockT := &testing.T{}
	slice := []string{"apple", "banana", "cherry"}
	AssertSliceContains(mockT, slice, "banana", "contains check")

	if mockT.Failed() {
		t.Error("AssertSliceContains should not have failed")
	}
}

func TestAssertSliceNotContains(t *testing.T) {
	mockT := &testing.T{}
	slice := []string{"apple", "banana", "cherry"}
	AssertSliceNotContains(mockT, slice, "grape", "not contains check")

	if mockT.Failed() {
		t.Error("AssertSliceNotContains should not have failed")
	}
}

// ========================================
// Map Helper Tests
// ========================================

func TestAssertMapLen(t *testing.T) {
	mockT := &testing.T{}
	m := map[string]int{"a": 1, "b": 2}
	AssertMapLen(mockT, m, 2, "map length check")

	if mockT.Failed() {
		t.Error("AssertMapLen should not have failed")
	}
}

func TestAssertMapContainsKey(t *testing.T) {
	mockT := &testing.T{}
	m := map[string]int{"key1": 100, "key2": 200}
	AssertMapContainsKey(mockT, m, "key1", "contains key check")

	if mockT.Failed() {
		t.Error("AssertMapContainsKey should not have failed")
	}
}

func TestAssertMapKeyValue(t *testing.T) {
	mockT := &testing.T{}
	m := map[string]int{"key1": 100, "key2": 200}
	AssertMapKeyValue(mockT, m, "key1", 100, "key value check")

	if mockT.Failed() {
		t.Error("AssertMapKeyValue should not have failed")
	}
}

// ========================================
// Time Helper Tests
// ========================================

func TestAssertTimeAfter(t *testing.T) {
	mockT := &testing.T{}
	earlier := time.Now()
	later := earlier.Add(time.Hour)
	AssertTimeAfter(mockT, later, earlier, "time after check")

	if mockT.Failed() {
		t.Error("AssertTimeAfter should not have failed")
	}
}

func TestAssertTimeBefore(t *testing.T) {
	mockT := &testing.T{}
	earlier := time.Now()
	later := earlier.Add(time.Hour)
	AssertTimeBefore(mockT, earlier, later, "time before check")

	if mockT.Failed() {
		t.Error("AssertTimeBefore should not have failed")
	}
}

func TestAssertTimeWithin(t *testing.T) {
	mockT := &testing.T{}
	base := time.Now()
	actual := base.Add(500 * time.Millisecond)
	AssertTimeWithin(mockT, actual, base, time.Second, "time within check")

	if mockT.Failed() {
		t.Error("AssertTimeWithin should not have failed")
	}
}

// ========================================
// Boolean Helper Tests
// ========================================

func TestAssertTrue(t *testing.T) {
	mockT := &testing.T{}
	AssertTrue(mockT, true, "true check")

	if mockT.Failed() {
		t.Error("AssertTrue should not have failed")
	}
}

func TestAssertFalse(t *testing.T) {
	mockT := &testing.T{}
	AssertFalse(mockT, false, "false check")

	if mockT.Failed() {
		t.Error("AssertFalse should not have failed")
	}
}

// ========================================
// Benchmarks
// ========================================

func BenchmarkAssertJSONEqual(b *testing.B) {
	expected := `{"name": "test", "count": 5, "nested": {"key": "value"}}`
	actual := `{"count":5,"name":"test","nested":{"key":"value"}}`

	for i := 0; i < b.N; i++ {
		mockT := &testing.T{}
		AssertJSONEqual(mockT, expected, actual, "benchmark")
	}
}

func BenchmarkTempTestDir(b *testing.B) {
	mockT := &testing.T{}
	for i := 0; i < b.N; i++ {
		dir, cleanup := TempTestDir(mockT, "bench-")
		cleanup()
		_ = dir
	}
}

func BenchmarkWriteTestFile(b *testing.B) {
	mockT := &testing.T{}
	dir, cleanup := TempTestDir(mockT, "bench-")
	defer cleanup()

	content := "benchmark test content"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		WriteTestFile(mockT, dir, "test.txt", content)
	}
}

func BenchmarkConcurrentTest(b *testing.B) {
	mockT := &testing.T{}
	for i := 0; i < b.N; i++ {
		ConcurrentTest(mockT, 10, func(workerID int) {
			// Minimal work
		})
	}
}

func BenchmarkAssertSliceContains(b *testing.B) {
	slice := make([]int, 100)
	for i := range slice {
		slice[i] = i
	}

	mockT := &testing.T{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AssertSliceContains(mockT, slice, 50, "benchmark")
	}
}

func BenchmarkAssertMapKeyValue(b *testing.B) {
	m := make(map[string]int)
	for i := 0; i < 100; i++ {
		m[string(rune('a'+i))] = i
	}

	mockT := &testing.T{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AssertMapKeyValue(mockT, m, "a", 0, "benchmark")
	}
}

// ========================================
// Error Helper Tests
// ========================================

func TestAssertErrorContains_Success(t *testing.T) {
	mockT := &testing.T{}
	err := os.ErrNotExist
	AssertErrorContains(mockT, err, "not exist", "should find substring")

	if mockT.Failed() {
		t.Error("AssertErrorContains should not have failed")
	}
}

func TestAssertErrorContains_NilError(t *testing.T) {
	mockT := &testing.T{}
	AssertErrorContains(mockT, nil, "any", "nil error check")

	if !mockT.Failed() {
		t.Error("AssertErrorContains should have failed for nil error")
	}
}

func TestAssertPanics_Success(t *testing.T) {
	mockT := &testing.T{}
	AssertPanics(mockT, func() {
		panic("test panic")
	}, "should panic")

	if mockT.Failed() {
		t.Error("AssertPanics should not have failed when function panics")
	}
}

func TestAssertPanics_NoPanic(t *testing.T) {
	mockT := &testing.T{}
	AssertPanics(mockT, func() {
		// No panic
	}, "should panic")

	if !mockT.Failed() {
		t.Error("AssertPanics should have failed when function does not panic")
	}
}

func TestAssertNoPanic_Success(t *testing.T) {
	mockT := &testing.T{}
	AssertNoPanic(mockT, func() {
		// Normal function
	}, "should not panic")

	if mockT.Failed() {
		t.Error("AssertNoPanic should not have failed")
	}
}

// ========================================
// Retry Helper Tests
// ========================================

func TestRetryUntil_ImmediateSuccess(t *testing.T) {
	result := RetryUntil(t, time.Second, 10*time.Millisecond, func() bool {
		return true
	}, "immediate success")

	if !result {
		t.Error("RetryUntil should have returned true")
	}
}

func TestRetryUntil_EventualSuccess(t *testing.T) {
	attempts := 0
	result := RetryUntil(t, time.Second, 10*time.Millisecond, func() bool {
		attempts++
		return attempts >= 3
	}, "eventual success")

	if !result {
		t.Error("RetryUntil should have returned true")
	}
	if attempts < 3 {
		t.Errorf("Expected at least 3 attempts, got %d", attempts)
	}
}

func TestRetryUntil_Timeout(t *testing.T) {
	result := RetryUntil(t, 50*time.Millisecond, 10*time.Millisecond, func() bool {
		return false
	}, "timeout expected")

	if result {
		t.Error("RetryUntil should have returned false on timeout")
	}
}

// ========================================
// Environment Helper Tests
// ========================================

func TestWithEnv(t *testing.T) {
	key := "TEST_HELPER_ENV_VAR"
	os.Unsetenv(key) // Ensure it's not set

	cleanup := WithEnv(t, key, "test-value")

	if got := os.Getenv(key); got != "test-value" {
		t.Errorf("expected env var to be 'test-value', got %q", got)
	}

	cleanup()

	if got := os.Getenv(key); got != "" {
		t.Errorf("expected env var to be unset after cleanup, got %q", got)
	}
}

func TestWithEnv_RestoresOriginal(t *testing.T) {
	key := "TEST_HELPER_ENV_VAR_RESTORE"
	os.Setenv(key, "original")

	cleanup := WithEnv(t, key, "modified")

	if got := os.Getenv(key); got != "modified" {
		t.Errorf("expected env var to be 'modified', got %q", got)
	}

	cleanup()

	if got := os.Getenv(key); got != "original" {
		t.Errorf("expected env var to be 'original' after cleanup, got %q", got)
	}

	os.Unsetenv(key)
}

func TestWithEnvs(t *testing.T) {
	envs := map[string]string{
		"TEST_MULTI_1": "value1",
		"TEST_MULTI_2": "value2",
	}

	cleanup := WithEnvs(t, envs)

	for k, v := range envs {
		if got := os.Getenv(k); got != v {
			t.Errorf("expected %s=%q, got %q", k, v, got)
		}
	}

	cleanup()

	for k := range envs {
		if got := os.Getenv(k); got != "" {
			t.Errorf("expected %s to be unset after cleanup, got %q", k, got)
		}
	}
}

// ========================================
// Call Counter Tests
// ========================================

func TestCallCounter(t *testing.T) {
	counter := NewCallCounter()

	if counter.Count() != 0 {
		t.Errorf("expected initial count 0, got %d", counter.Count())
	}

	counter.Inc()
	counter.Inc()
	counter.Inc()

	if counter.Count() != 3 {
		t.Errorf("expected count 3, got %d", counter.Count())
	}

	counter.Reset()

	if counter.Count() != 0 {
		t.Errorf("expected count 0 after reset, got %d", counter.Count())
	}
}

func TestCallCounter_Concurrent(t *testing.T) {
	counter := NewCallCounter()

	ConcurrentTest(t, 100, func(workerID int) {
		counter.Inc()
	})

	if counter.Count() != 100 {
		t.Errorf("expected count 100, got %d", counter.Count())
	}
}

func TestCallCounter_AssertCount(t *testing.T) {
	mockT := &testing.T{}
	counter := NewCallCounter()
	counter.Inc()
	counter.Inc()

	counter.AssertCount(mockT, 2, "should be 2")

	if mockT.Failed() {
		t.Error("AssertCount should not have failed")
	}
}

// ========================================
// Deep Equal Tests
// ========================================

func TestAssertDeepEqual_Success(t *testing.T) {
	mockT := &testing.T{}

	type Nested struct {
		Value string `json:"value"`
	}
	type TestStruct struct {
		Name   string   `json:"name"`
		Count  int      `json:"count"`
		Nested Nested   `json:"nested"`
		Tags   []string `json:"tags"`
	}

	expected := TestStruct{
		Name:   "test",
		Count:  5,
		Nested: Nested{Value: "inner"},
		Tags:   []string{"a", "b"},
	}
	actual := TestStruct{
		Name:   "test",
		Count:  5,
		Nested: Nested{Value: "inner"},
		Tags:   []string{"a", "b"},
	}

	AssertDeepEqual(mockT, expected, actual, "structs should be equal")

	if mockT.Failed() {
		t.Error("AssertDeepEqual should not have failed for equal structs")
	}
}

// ========================================
// HTTP Helper Tests
// ========================================

func TestAssertStatusCode(t *testing.T) {
	mockT := &testing.T{}
	AssertStatusCode(mockT, 200, 200, "status check")

	if mockT.Failed() {
		t.Error("AssertStatusCode should not have failed")
	}
}

func TestAssertContentType(t *testing.T) {
	mockT := &testing.T{}
	AssertContentType(mockT, "application/json; charset=utf-8", "application/json", "content type check")

	if mockT.Failed() {
		t.Error("AssertContentType should not have failed")
	}
}

// ========================================
// Benchmarks for New Utilities
// ========================================

func BenchmarkCallCounter_Inc(b *testing.B) {
	counter := NewCallCounter()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		counter.Inc()
	}
}

func BenchmarkCallCounter_Concurrent(b *testing.B) {
	counter := NewCallCounter()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			counter.Inc()
		}
	})
}

func BenchmarkWithEnv(b *testing.B) {
	mockT := &testing.T{}
	key := "BENCH_ENV_VAR"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cleanup := WithEnv(mockT, key, "value")
		cleanup()
	}
}

func BenchmarkAssertDeepEqual(b *testing.B) {
	type TestStruct struct {
		Name  string   `json:"name"`
		Count int      `json:"count"`
		Tags  []string `json:"tags"`
	}

	expected := TestStruct{Name: "test", Count: 5, Tags: []string{"a", "b", "c"}}
	actual := TestStruct{Name: "test", Count: 5, Tags: []string{"a", "b", "c"}}
	mockT := &testing.T{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AssertDeepEqual(mockT, expected, actual, "bench")
	}
}
