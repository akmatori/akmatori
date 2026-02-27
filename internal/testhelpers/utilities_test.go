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
