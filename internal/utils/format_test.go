package utils

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"milliseconds", 45 * time.Millisecond, "45ms"},
		{"under second", 500 * time.Millisecond, "500ms"},
		{"one second", 1 * time.Second, "1.0s"},
		{"seconds with decimal", 1500 * time.Millisecond, "1.5s"},
		{"under minute", 45 * time.Second, "45.0s"},
		{"one minute", 1 * time.Minute, "1m"},
		{"minutes and seconds", 2*time.Minute + 30*time.Second, "2m 30s"},
		{"just minutes", 5 * time.Minute, "5m"},
		{"one hour", 1 * time.Hour, "1h"},
		{"hours and minutes", 1*time.Hour + 15*time.Minute, "1h 15m"},
		{"just hours", 2 * time.Hour, "2h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("FormatDuration(%v) = %s; want %s", tt.duration, result, tt.expected)
			}
		})
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		name     string
		number   int
		expected string
	}{
		{"zero", 0, "0"},
		{"single digit", 5, "5"},
		{"double digit", 42, "42"},
		{"triple digit", 123, "123"},
		{"thousands", 1234, "1,234"},
		{"ten thousands", 12345, "12,345"},
		{"hundred thousands", 123456, "123,456"},
		{"millions", 1234567, "1,234,567"},
		{"ten millions", 12345678, "12,345,678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatNumber(tt.number)
			if result != tt.expected {
				t.Errorf("FormatNumber(%d) = %s; want %s", tt.number, result, tt.expected)
			}
		})
	}
}

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxLen   int
		expected string
	}{
		{"empty string", "", 10, ""},
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"very short max", "hello", 3, "..."},
		{"with newlines", "hello\nworld", 20, "hello world"},
		{"multiline truncate", "hello\nworld\nfoo", 10, "hello w..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TruncateText(tt.text, tt.maxLen)
			if result != tt.expected {
				t.Errorf("TruncateText(%q, %d) = %q; want %q", tt.text, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestGetLastNLines(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		n        int
		expected string
	}{
		{"empty string", "", 5, ""},
		{"single line", "hello", 5, "hello"},
		{"fewer lines than n", "line1\nline2", 5, "line1\nline2"},
		{"exact n lines", "line1\nline2\nline3", 3, "line1\nline2\nline3"},
		{"more lines than n", "line1\nline2\nline3\nline4\nline5", 3, "line3\nline4\nline5"},
		{"get last 1", "a\nb\nc\nd", 1, "d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetLastNLines(tt.text, tt.n)
			if result != tt.expected {
				t.Errorf("GetLastNLines(%q, %d) = %q; want %q", tt.text, tt.n, result, tt.expected)
			}
		})
	}
}

func TestMinMax(t *testing.T) {
	t.Run("Max", func(t *testing.T) {
		if Max(5, 3) != 5 {
			t.Error("Max(5, 3) should be 5")
		}
		if Max(3, 5) != 5 {
			t.Error("Max(3, 5) should be 5")
		}
		if Max(5, 5) != 5 {
			t.Error("Max(5, 5) should be 5")
		}
		if Max(-1, -5) != -1 {
			t.Error("Max(-1, -5) should be -1")
		}
	})

	t.Run("Min", func(t *testing.T) {
		if Min(5, 3) != 3 {
			t.Error("Min(5, 3) should be 3")
		}
		if Min(3, 5) != 3 {
			t.Error("Min(3, 5) should be 3")
		}
		if Min(5, 5) != 5 {
			t.Error("Min(5, 5) should be 5")
		}
		if Min(-1, -5) != -5 {
			t.Error("Min(-1, -5) should be -5")
		}
	})
}

func TestTruncateLogForSlack(t *testing.T) {
	tests := []struct {
		name     string
		log      string
		maxLen   int
		contains string
	}{
		{"short log", "short log", 100, "short log"},
		{"exact length", "exactly", 7, "exactly"},
		{"truncated", "line1\nline2\nline3\nline4\nline5", 20, "truncated"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TruncateLogForSlack(tt.log, tt.maxLen)
			if len(result) > tt.maxLen+20 { // Allow for "...(truncated)\n" prefix
				t.Errorf("Result too long: got %d, max was %d", len(result), tt.maxLen)
			}
		})
	}
}

func TestAppendMetrics(t *testing.T) {
	t.Run("with tokens", func(t *testing.T) {
		result := AppendMetrics("output", 5*time.Second, 1500)
		if result != "output\n\n---\n⏱️ Time: 5.0s | 🎯 Tokens: 1,500" {
			t.Errorf("Unexpected result: %s", result)
		}
	})

	t.Run("without tokens", func(t *testing.T) {
		result := AppendMetrics("output", 2*time.Minute, 0)
		if result != "output\n\n---\n⏱️ Time: 2m" {
			t.Errorf("Unexpected result: %s", result)
		}
	})
}
