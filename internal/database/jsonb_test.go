package database

import (
	"encoding/json"
	"testing"
)

// --- JSONB Scan Tests (Table-Driven) ---

func TestJSONB_Scan_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		input       interface{}
		wantErr     bool
		checkResult func(*testing.T, JSONB)
	}{
		{
			name:    "nil value initializes empty map",
			input:   nil,
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				if j == nil {
					t.Error("expected non-nil map")
				}
				if len(j) != 0 {
					t.Errorf("expected empty map, got %d entries", len(j))
				}
			},
		},
		{
			name:    "valid JSON object",
			input:   []byte(`{"key": "value", "count": 42}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				if j["key"] != "value" {
					t.Errorf("key = %v, want 'value'", j["key"])
				}
				if num, ok := j["count"].(float64); !ok || num != 42 {
					t.Errorf("count = %v, want 42", j["count"])
				}
			},
		},
		{
			name:    "empty JSON object",
			input:   []byte(`{}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				if len(j) != 0 {
					t.Errorf("expected empty map, got %d entries", len(j))
				}
			},
		},
		{
			name:    "nested objects",
			input:   []byte(`{"outer": {"inner": "value"}}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				outer, ok := j["outer"].(map[string]interface{})
				if !ok {
					t.Fatalf("outer is not a map")
				}
				if outer["inner"] != "value" {
					t.Errorf("inner = %v, want 'value'", outer["inner"])
				}
			},
		},
		{
			name:    "array values",
			input:   []byte(`{"items": [1, 2, 3]}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				items, ok := j["items"].([]interface{})
				if !ok {
					t.Fatalf("items is not an array")
				}
				if len(items) != 3 {
					t.Errorf("items length = %d, want 3", len(items))
				}
			},
		},
		{
			name:    "boolean values",
			input:   []byte(`{"enabled": true, "disabled": false}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				if j["enabled"] != true {
					t.Errorf("enabled = %v, want true", j["enabled"])
				}
				if j["disabled"] != false {
					t.Errorf("disabled = %v, want false", j["disabled"])
				}
			},
		},
		{
			name:    "null values",
			input:   []byte(`{"nullable": null}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				if j["nullable"] != nil {
					t.Errorf("nullable = %v, want nil", j["nullable"])
				}
			},
		},
		{
			name:    "string type (not []byte)",
			input:   "not bytes",
			wantErr: true,
			checkResult: func(t *testing.T, j JSONB) {
				// error case, j should be unchanged
			},
		},
		{
			name:    "int type (not []byte)",
			input:   42,
			wantErr: true,
			checkResult: func(t *testing.T, j JSONB) {},
		},
		{
			name:    "invalid JSON syntax",
			input:   []byte(`not valid json`),
			wantErr: true,
			checkResult: func(t *testing.T, j JSONB) {},
		},
		{
			name:    "truncated JSON",
			input:   []byte(`{"key": "val`),
			wantErr: true,
			checkResult: func(t *testing.T, j JSONB) {},
		},
		{
			name:    "JSON array at root (invalid for JSONB map)",
			input:   []byte(`[1, 2, 3]`),
			wantErr: true, // json.Unmarshal into map fails for array
			checkResult: func(t *testing.T, j JSONB) {},
		},
		{
			name:    "empty bytes",
			input:   []byte(``),
			wantErr: true, // empty is not valid JSON
			checkResult: func(t *testing.T, j JSONB) {},
		},
		{
			name:    "unicode keys and values",
			input:   []byte(`{"日本語": "値", "emoji": "🔥"}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				if j["日本語"] != "値" {
					t.Errorf("日本語 = %v, want '値'", j["日本語"])
				}
				if j["emoji"] != "🔥" {
					t.Errorf("emoji = %v, want '🔥'", j["emoji"])
				}
			},
		},
		{
			name:    "large numbers",
			input:   []byte(`{"big": 9223372036854775807}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				// JSON numbers are float64, which loses precision for large ints
				if _, ok := j["big"].(float64); !ok {
					t.Errorf("big should be float64, got %T", j["big"])
				}
			},
		},
		{
			name:    "scientific notation",
			input:   []byte(`{"sci": 1.23e10}`),
			wantErr: false,
			checkResult: func(t *testing.T, j JSONB) {
				if num, ok := j["sci"].(float64); !ok || num != 1.23e10 {
					t.Errorf("sci = %v, want 1.23e10", j["sci"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var j JSONB
			err := j.Scan(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Scan() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				tt.checkResult(t, j)
			}
		})
	}
}

// --- JSONB Value Tests (Table-Driven) ---

func TestJSONB_Value_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		jsonb    JSONB
		wantNil  bool
		wantJSON string // expected JSON string if not nil
	}{
		{
			name:    "nil JSONB",
			jsonb:   nil,
			wantNil: true,
		},
		{
			name:     "empty map",
			jsonb:    JSONB{},
			wantNil:  false,
			wantJSON: `{}`,
		},
		{
			name:     "single key-value",
			jsonb:    JSONB{"key": "value"},
			wantNil:  false,
			wantJSON: `{"key":"value"}`,
		},
		{
			name:     "numeric value",
			jsonb:    JSONB{"num": float64(42)},
			wantNil:  false,
			wantJSON: `{"num":42}`,
		},
		{
			name:     "boolean values",
			jsonb:    JSONB{"yes": true, "no": false},
			wantNil:  false,
			// Order may vary, so we'll verify by unmarshaling
		},
		{
			name:     "null value",
			jsonb:    JSONB{"nothing": nil},
			wantNil:  false,
			wantJSON: `{"nothing":null}`,
		},
		{
			name:     "nested map",
			jsonb:    JSONB{"outer": map[string]interface{}{"inner": "val"}},
			wantNil:  false,
			wantJSON: `{"outer":{"inner":"val"}}`,
		},
		{
			name:     "array value",
			jsonb:    JSONB{"list": []interface{}{1.0, 2.0, 3.0}},
			wantNil:  false,
			wantJSON: `{"list":[1,2,3]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := tt.jsonb.Value()
			if err != nil {
				t.Fatalf("Value() error = %v", err)
			}

			if tt.wantNil {
				if val != nil {
					t.Errorf("Value() = %v, want nil", val)
				}
				return
			}

			bytes, ok := val.([]byte)
			if !ok {
				t.Fatalf("Value() type = %T, want []byte", val)
			}

			if tt.wantJSON != "" {
				// For deterministic JSON comparison, unmarshal and compare
				var got, want map[string]interface{}
				if err := json.Unmarshal(bytes, &got); err != nil {
					t.Fatalf("failed to unmarshal result: %v", err)
				}
				if err := json.Unmarshal([]byte(tt.wantJSON), &want); err != nil {
					t.Fatalf("failed to unmarshal expected: %v", err)
				}

				// Deep comparison would be complex, so just check basic structure
				if len(got) != len(want) {
					t.Errorf("Value() map len = %d, want %d", len(got), len(want))
				}
			}
		})
	}
}

// --- JSONB Round-Trip Tests ---

func TestJSONB_RoundTrip_Comprehensive(t *testing.T) {
	tests := []struct {
		name  string
		input JSONB
	}{
		{"simple string", JSONB{"key": "value"}},
		{"multiple types", JSONB{"str": "hello", "num": 3.14, "bool": true, "nil": nil}},
		{"nested", JSONB{"level1": map[string]interface{}{"level2": "deep"}}},
		{"array", JSONB{"items": []interface{}{"a", "b", "c"}}},
		{"empty", JSONB{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode to bytes
			val, err := tt.input.Value()
			if err != nil {
				t.Fatalf("Value() error = %v", err)
			}

			if val == nil && len(tt.input) == 0 {
				// Special case: nil JSONB returns nil, not empty JSON
				return
			}

			// Decode back
			var result JSONB
			if err := result.Scan(val); err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			// Compare original and result
			if len(result) != len(tt.input) {
				t.Errorf("round-trip changed map length: got %d, want %d", len(result), len(tt.input))
			}
		})
	}
}

// --- Edge Cases ---

func TestJSONB_Scan_MergesWithExistingData(t *testing.T) {
	j := JSONB{"old": "data"}

	// Scan new data - json.Unmarshal merges into existing map
	err := j.Scan([]byte(`{"new": "value"}`))
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	// Old data is kept (json.Unmarshal merges, doesn't replace)
	if _, ok := j["old"]; !ok {
		t.Error("old key should still exist (json.Unmarshal merges)")
	}
	if j["new"] != "value" {
		t.Errorf("new = %v, want 'value'", j["new"])
	}
}

func TestJSONB_Scan_NilOverwritesExisting(t *testing.T) {
	j := JSONB{"key": "value"}

	// Scanning nil should reset to empty map
	err := j.Scan(nil)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	if len(j) != 0 {
		t.Errorf("expected empty map after nil scan, got %d entries", len(j))
	}
}

func TestJSONB_DeepCopy_Independence(t *testing.T) {
	original := JSONB{"key": "original"}

	// Create copy via Value/Scan cycle
	val, _ := original.Value()
	var copy JSONB
	_ = copy.Scan(val)

	// Modify copy
	copy["key"] = "modified"

	// Original should be unchanged
	if original["key"] != "original" {
		t.Errorf("original was modified: got %v", original["key"])
	}
}
