package output

import (
	"strings"
	"testing"
)

// --- titleCase tests ---

func TestTitleCase_SingleWord(t *testing.T) {
	if got := titleCase("status"); got != "Status" {
		t.Errorf("titleCase(%q) = %q, want %q", "status", got, "Status")
	}
}

func TestTitleCase_SnakeCase(t *testing.T) {
	if got := titleCase("actions_taken"); got != "Actions Taken" {
		t.Errorf("titleCase(%q) = %q, want %q", "actions_taken", got, "Actions Taken")
	}
}

func TestTitleCase_KebabCase(t *testing.T) {
	if got := titleCase("my-field-name"); got != "My Field Name" {
		t.Errorf("titleCase(%q) = %q, want %q", "my-field-name", got, "My Field Name")
	}
}

func TestTitleCase_Mixed(t *testing.T) {
	if got := titleCase("my_field-name"); got != "My Field Name" {
		t.Errorf("titleCase(%q) = %q, want %q", "my_field-name", got, "My Field Name")
	}
}

func TestTitleCase_AlreadyCapitalized(t *testing.T) {
	if got := titleCase("Summary"); got != "Summary" {
		t.Errorf("titleCase(%q) = %q, want %q", "Summary", got, "Summary")
	}
}

// --- RenderForSlack scalar tests ---

func TestRenderForSlack_ScalarString(t *testing.T) {
	specs := []FieldSpec{{Name: "summary", Kind: "string"}}
	parsed := map[string]any{"summary": "An incident occurred."}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "*Summary:* An incident occurred.") {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestRenderForSlack_ScalarNumber(t *testing.T) {
	specs := []FieldSpec{{Name: "count", Kind: "number"}}
	parsed := map[string]any{"count": float64(42)}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "*Count:* 42") {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestRenderForSlack_ScalarBool(t *testing.T) {
	specs := []FieldSpec{{Name: "active", Kind: "bool"}}
	parsed := map[string]any{"active": true}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "*Active:* true") {
		t.Errorf("unexpected output: %q", got)
	}
}

// --- Status emoji tests ---

func TestRenderForSlack_StatusResolved(t *testing.T) {
	specs := []FieldSpec{{Name: "status", Kind: "string"}}
	parsed := map[string]any{"status": "resolved"}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "✅") {
		t.Errorf("expected ✅ emoji for resolved status, got %q", got)
	}
	if !strings.Contains(got, "resolved") {
		t.Errorf("expected value in output, got %q", got)
	}
}

func TestRenderForSlack_StatusUnresolved(t *testing.T) {
	specs := []FieldSpec{{Name: "status", Kind: "string"}}
	parsed := map[string]any{"status": "unresolved"}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "⚠️") {
		t.Errorf("expected ⚠️ emoji for unresolved status, got %q", got)
	}
}

func TestRenderForSlack_StatusEscalate(t *testing.T) {
	specs := []FieldSpec{{Name: "status", Kind: "string"}}
	parsed := map[string]any{"status": "escalate"}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "🚨") {
		t.Errorf("expected 🚨 emoji for escalate status, got %q", got)
	}
}

func TestRenderForSlack_StatusUnknownNoEmoji(t *testing.T) {
	specs := []FieldSpec{{Name: "status", Kind: "string"}}
	parsed := map[string]any{"status": "pending"}
	got := RenderForSlack(parsed, specs)
	// Should render without any of the known emojis
	if strings.Contains(got, "✅") || strings.Contains(got, "⚠️") || strings.Contains(got, "🚨") {
		t.Errorf("unexpected emoji for unknown status value, got %q", got)
	}
	if !strings.Contains(got, "pending") {
		t.Errorf("expected value in output, got %q", got)
	}
}

func TestRenderForSlack_NonStatusKeyNoEmoji(t *testing.T) {
	// A key named "severity" with value "resolved" should not get an emoji
	specs := []FieldSpec{{Name: "severity", Kind: "string"}}
	parsed := map[string]any{"severity": "resolved"}
	got := RenderForSlack(parsed, specs)
	if strings.Contains(got, "✅") {
		t.Errorf("emoji should not appear for non-status key with resolved value, got %q", got)
	}
	if !strings.Contains(got, "resolved") {
		t.Errorf("expected value in output, got %q", got)
	}
}

// --- List of scalars tests ---

func TestRenderForSlack_ListStringPopulated(t *testing.T) {
	specs := []FieldSpec{{Name: "actions_taken", Kind: "list_string"}}
	parsed := map[string]any{"actions_taken": []any{"restarted service", "cleared queue"}}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "*Actions Taken:*") {
		t.Errorf("expected heading, got %q", got)
	}
	if !strings.Contains(got, " • restarted service") {
		t.Errorf("expected bullet item, got %q", got)
	}
	if !strings.Contains(got, " • cleared queue") {
		t.Errorf("expected second bullet item, got %q", got)
	}
}

func TestRenderForSlack_ListStringEmpty(t *testing.T) {
	specs := []FieldSpec{{Name: "recommendations", Kind: "list_string"}}
	parsed := map[string]any{"recommendations": []any{}}
	got := RenderForSlack(parsed, specs)
	if got != "" {
		t.Errorf("expected empty string for empty list, got %q", got)
	}
}

func TestRenderForSlack_ListNumberPopulated(t *testing.T) {
	specs := []FieldSpec{{Name: "counts", Kind: "list_number"}}
	parsed := map[string]any{"counts": []any{float64(1), float64(2), float64(3)}}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "*Counts:*") {
		t.Errorf("expected heading, got %q", got)
	}
	if !strings.Contains(got, " • 1") {
		t.Errorf("expected bullet item for 1, got %q", got)
	}
}

// --- List of objects tests ---

func TestRenderForSlack_ListObject(t *testing.T) {
	specs := []FieldSpec{
		{
			Name: "hosts",
			Kind: "list_object",
			Children: []FieldSpec{
				{Name: "name", Kind: "string"},
				{Name: "port", Kind: "number"},
			},
		},
	}
	parsed := map[string]any{
		"hosts": []any{
			map[string]any{"name": "server1", "port": float64(8080)},
			map[string]any{"name": "server2", "port": float64(9090)},
		},
	}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "*Hosts:*") {
		t.Errorf("expected heading, got %q", got)
	}
	if !strings.Contains(got, "Name: server1") {
		t.Errorf("expected Name field, got %q", got)
	}
	if !strings.Contains(got, "Port: 8080") {
		t.Errorf("expected Port field, got %q", got)
	}
	if !strings.Contains(got, "server2") {
		t.Errorf("expected second object, got %q", got)
	}
}

func TestRenderForSlack_ListObjectEmpty(t *testing.T) {
	specs := []FieldSpec{
		{Name: "hosts", Kind: "list_object", Children: []FieldSpec{{Name: "name", Kind: "string"}}},
	}
	parsed := map[string]any{"hosts": []any{}}
	got := RenderForSlack(parsed, specs)
	if got != "" {
		t.Errorf("expected empty string for empty list_object, got %q", got)
	}
}

// --- Nested object tests ---

func TestRenderForSlack_NestedObject(t *testing.T) {
	specs := []FieldSpec{
		{
			Name: "meta",
			Kind: "object",
			Children: []FieldSpec{
				{Name: "host", Kind: "string"},
				{Name: "count", Kind: "number"},
			},
		},
	}
	parsed := map[string]any{
		"meta": map[string]any{
			"host":  "server1",
			"count": float64(42),
		},
	}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "*Meta:*") {
		t.Errorf("expected heading, got %q", got)
	}
	if !strings.Contains(got, " • Host: server1") {
		t.Errorf("expected Host child, got %q", got)
	}
	if !strings.Contains(got, " • Count: 42") {
		t.Errorf("expected Count child, got %q", got)
	}
}

// --- Mixed shapes test ---

func TestRenderForSlack_MixedShapes(t *testing.T) {
	specs := []FieldSpec{
		{Name: "status", Kind: "string"},
		{Name: "summary", Kind: "string"},
		{Name: "actions_taken", Kind: "list_string"},
		{Name: "recommendations", Kind: "list_string"},
	}
	parsed := map[string]any{
		"status":          "resolved",
		"summary":         "Service recovered.",
		"actions_taken":   []any{"restarted pod"},
		"recommendations": []any{},
	}
	got := RenderForSlack(parsed, specs)
	if !strings.Contains(got, "✅") {
		t.Errorf("expected status emoji, got %q", got)
	}
	if !strings.Contains(got, "*Summary:* Service recovered.") {
		t.Errorf("expected summary, got %q", got)
	}
	if !strings.Contains(got, " • restarted pod") {
		t.Errorf("expected action bullet, got %q", got)
	}
	// Empty recommendations list should not appear
	if strings.Contains(got, "*Recommendations:*") {
		t.Errorf("empty list should be omitted, got %q", got)
	}
}

// --- Order preservation test ---

func TestRenderForSlack_PreservesSpecOrder(t *testing.T) {
	specs := []FieldSpec{
		{Name: "z_field", Kind: "string"},
		{Name: "a_field", Kind: "string"},
	}
	parsed := map[string]any{
		"z_field": "first",
		"a_field": "second",
	}
	got := RenderForSlack(parsed, specs)
	zIdx := strings.Index(got, "Z Field")
	aIdx := strings.Index(got, "A Field")
	if zIdx < 0 || aIdx < 0 {
		t.Fatalf("expected both fields in output, got %q", got)
	}
	if zIdx > aIdx {
		t.Errorf("spec order not preserved: Z Field should come before A Field, got %q", got)
	}
}

// --- Empty return tests ---

func TestRenderForSlack_AllEmptyLists(t *testing.T) {
	specs := []FieldSpec{
		{Name: "items", Kind: "list_string"},
		{Name: "objects", Kind: "list_object", Children: []FieldSpec{{Name: "id", Kind: "string"}}},
	}
	parsed := map[string]any{
		"items":   []any{},
		"objects": []any{},
	}
	got := RenderForSlack(parsed, specs)
	if got != "" {
		t.Errorf("expected empty string when all lists are empty, got %q", got)
	}
}

func TestRenderForSlack_MissingKey(t *testing.T) {
	specs := []FieldSpec{{Name: "status", Kind: "string"}}
	parsed := map[string]any{}
	got := RenderForSlack(parsed, specs)
	if got != "" {
		t.Errorf("expected empty string for missing key, got %q", got)
	}
}
