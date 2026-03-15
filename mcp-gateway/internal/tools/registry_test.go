package tools

import (
	"testing"
)

func TestExtractInstanceID(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want *uint
	}{
		{"present", map[string]interface{}{"tool_instance_id": float64(5)}, uintPtr(5)},
		{"zero", map[string]interface{}{"tool_instance_id": float64(0)}, nil},
		{"missing", map[string]interface{}{}, nil},
		{"wrong type", map[string]interface{}{"tool_instance_id": "5"}, nil},
		{"negative", map[string]interface{}{"tool_instance_id": float64(-1)}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractInstanceID(tt.args)
			if tt.want == nil {
				if got != nil {
					t.Errorf("extractInstanceID() = %v, want nil", *got)
				}
			} else {
				if got == nil {
					t.Errorf("extractInstanceID() = nil, want %d", *tt.want)
				} else if *got != *tt.want {
					t.Errorf("extractInstanceID() = %d, want %d", *got, *tt.want)
				}
			}
		})
	}
}

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"present", map[string]interface{}{"logical_name": "prod-ssh"}, "prod-ssh"},
		{"empty string", map[string]interface{}{"logical_name": ""}, ""},
		{"missing", map[string]interface{}{}, ""},
		{"wrong type", map[string]interface{}{"logical_name": 123}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLogicalName(tt.args)
			if got != tt.want {
				t.Errorf("extractLogicalName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractServers(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want []string
	}{
		{"present", map[string]interface{}{"servers": []interface{}{"a", "b"}}, []string{"a", "b"}},
		{"missing", map[string]interface{}{}, nil},
		{"empty", map[string]interface{}{"servers": []interface{}{}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractServers(tt.args)
			if len(got) != len(tt.want) {
				t.Errorf("extractServers() length = %d, want %d", len(got), len(tt.want))
				return
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("extractServers()[%d] = %q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

func uintPtr(v uint) *uint { return &v }
