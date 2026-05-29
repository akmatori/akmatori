package services

import (
	"strings"
	"testing"
)

// --- inferSchema tests ---

func TestInferSchema_Scalars(t *testing.T) {
	specs, err := inferSchema(`{"name":"Alice","age":30,"active":true}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("expected 3 specs, got %d: %+v", len(specs), specs)
	}
	want := []fieldSpec{
		{Name: "name", Kind: "string"},
		{Name: "age", Kind: "number"},
		{Name: "active", Kind: "bool"},
	}
	for i, w := range want {
		if specs[i].Name != w.Name || specs[i].Kind != w.Kind {
			t.Errorf("spec[%d]: want {%s, %s}, got {%s, %s}", i, w.Name, w.Kind, specs[i].Name, specs[i].Kind)
		}
	}
}

func TestInferSchema_ListOfStrings(t *testing.T) {
	specs, err := inferSchema(`{"tags":["alpha","beta","gamma"]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 || specs[0].Name != "tags" || specs[0].Kind != "list_string" {
		t.Errorf("expected list_string spec for tags, got %+v", specs)
	}
}

func TestInferSchema_ListOfNumbers(t *testing.T) {
	specs, err := inferSchema(`{"counts":[1,2,3]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 || specs[0].Kind != "list_number" {
		t.Errorf("expected list_number, got %+v", specs)
	}
}

func TestInferSchema_EmptyArray(t *testing.T) {
	specs, err := inferSchema(`{"items":[]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 || specs[0].Kind != "list_string" {
		t.Errorf("expected empty array to default to list_string, got %+v", specs)
	}
}

func TestInferSchema_ListOfObjects(t *testing.T) {
	specs, err := inferSchema(`{"hosts":[{"name":"server1","port":8080},{"name":"server2","port":9090}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	s := specs[0]
	if s.Name != "hosts" || s.Kind != "list_object" {
		t.Errorf("expected list_object for hosts, got {%s, %s}", s.Name, s.Kind)
	}
	if len(s.Children) != 2 {
		t.Fatalf("expected 2 children, got %d: %+v", len(s.Children), s.Children)
	}
	if s.Children[0].Name != "name" || s.Children[0].Kind != "string" {
		t.Errorf("child[0]: want {name, string}, got {%s, %s}", s.Children[0].Name, s.Children[0].Kind)
	}
	if s.Children[1].Name != "port" || s.Children[1].Kind != "number" {
		t.Errorf("child[1]: want {port, number}, got {%s, %s}", s.Children[1].Name, s.Children[1].Kind)
	}
}

func TestInferSchema_NestedObject(t *testing.T) {
	specs, err := inferSchema(`{"meta":{"host":"server1","count":42},"status":"ok"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	meta := specs[0]
	if meta.Name != "meta" || meta.Kind != "object" {
		t.Errorf("expected object for meta, got {%s, %s}", meta.Name, meta.Kind)
	}
	if len(meta.Children) != 2 {
		t.Fatalf("expected 2 children for meta, got %d", len(meta.Children))
	}
	if meta.Children[0].Name != "host" || meta.Children[0].Kind != "string" {
		t.Errorf("meta.children[0]: want {host, string}, got {%s, %s}", meta.Children[0].Name, meta.Children[0].Kind)
	}
	if meta.Children[1].Name != "count" || meta.Children[1].Kind != "number" {
		t.Errorf("meta.children[1]: want {count, number}, got {%s, %s}", meta.Children[1].Name, meta.Children[1].Kind)
	}
	if specs[1].Name != "status" || specs[1].Kind != "string" {
		t.Errorf("specs[1]: want {status, string}, got {%s, %s}", specs[1].Name, specs[1].Kind)
	}
}

func TestInferSchema_NonObjectTopLevel(t *testing.T) {
	cases := []string{
		`[]`,
		`"just a string"`,
		`42`,
		`true`,
		`null`,
	}
	for _, c := range cases {
		_, err := inferSchema(c)
		if err == nil {
			t.Errorf("expected error for non-object top-level %q, got nil", c)
		}
	}
}

func TestInferSchema_InvalidJSON(t *testing.T) {
	_, err := inferSchema("not json at all")
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestInferSchema_DefaultExampleIsValid(t *testing.T) {
	specs, err := inferSchema(defaultSchemaExample)
	if err != nil {
		t.Fatalf("built-in defaultSchemaExample failed to parse: %v", err)
	}
	if len(specs) != 4 {
		t.Errorf("expected 4 specs from defaultSchemaExample, got %d", len(specs))
	}
	// status and summary → string; actions_taken and recommendations → list_string
	kindWant := map[string]string{
		"status":          "string",
		"summary":         "string",
		"actions_taken":   "list_string",
		"recommendations": "list_string",
	}
	for _, s := range specs {
		if want, ok := kindWant[s.Name]; ok && s.Kind != want {
			t.Errorf("defaultSchemaExample field %q: want kind %s, got %s", s.Name, want, s.Kind)
		}
	}
}

func TestInferSchema_PreservesKeyOrder(t *testing.T) {
	specs, err := inferSchema(`{"z":"last","a":"first","m":"middle"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("expected 3 specs, got %d", len(specs))
	}
	if specs[0].Name != "z" || specs[1].Name != "a" || specs[2].Name != "m" {
		t.Errorf("key order not preserved: got %s, %s, %s", specs[0].Name, specs[1].Name, specs[2].Name)
	}
}

// --- buildSchemaInstruction tests ---

func TestBuildSchemaInstruction_ContainsDirective(t *testing.T) {
	instr := buildSchemaInstruction(`{"status":"ok"}`)
	if !strings.Contains(instr, "Return ONLY a single JSON object") {
		t.Errorf("expected instruction directive, got %q", instr)
	}
	if !strings.Contains(instr, `"status"`) {
		t.Errorf("expected field name in instruction, got %q", instr)
	}
}

func TestBuildSchemaInstruction_PrettyPrints(t *testing.T) {
	instr := buildSchemaInstruction(`{"a":1,"b":2}`)
	// Pretty-printed output should contain newlines and indentation.
	if !strings.Contains(instr, "\n") {
		t.Errorf("expected pretty-printed output with newlines, got %q", instr)
	}
}

// --- validateAgainstSpecs tests ---

func TestValidateAgainstSpecs_AllPassing(t *testing.T) {
	specs := []fieldSpec{
		{Name: "status", Kind: "string"},
		{Name: "count", Kind: "number"},
		{Name: "active", Kind: "bool"},
		{Name: "tags", Kind: "list_string"},
	}
	parsed := map[string]any{
		"status": "resolved",
		"count":  float64(3),
		"active": true,
		"tags":   []any{"a", "b"},
	}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateAgainstSpecs_MissingKey(t *testing.T) {
	specs := []fieldSpec{
		{Name: "status", Kind: "string"},
		{Name: "summary", Kind: "string"},
	}
	parsed := map[string]any{
		"status": "ok",
		// summary missing
	}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "summary") {
		t.Errorf("expected error to mention 'summary', got %q", errs[0])
	}
}

func TestValidateAgainstSpecs_WrongType(t *testing.T) {
	specs := []fieldSpec{{Name: "count", Kind: "number"}}
	parsed := map[string]any{"count": "not-a-number"}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) == 0 {
		t.Error("expected type error, got none")
	}
	if !strings.Contains(errs[0], "count") {
		t.Errorf("expected error to mention 'count', got %q", errs[0])
	}
}

func TestValidateAgainstSpecs_ExtraKeyTolerated(t *testing.T) {
	specs := []fieldSpec{{Name: "status", Kind: "string"}}
	parsed := map[string]any{
		"status": "ok",
		"extra":  "ignored",
	}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) != 0 {
		t.Errorf("expected no errors (extra keys tolerated), got %v", errs)
	}
}

func TestValidateAgainstSpecs_EmptyArrayPasses(t *testing.T) {
	specs := []fieldSpec{{Name: "items", Kind: "list_string"}}
	parsed := map[string]any{"items": []any{}}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) != 0 {
		t.Errorf("expected empty array to pass, got %v", errs)
	}
}

func TestValidateAgainstSpecs_ListStringWrongElementType(t *testing.T) {
	specs := []fieldSpec{{Name: "tags", Kind: "list_string"}}
	parsed := map[string]any{"tags": []any{"ok", float64(42)}}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) == 0 {
		t.Error("expected element type error, got none")
	}
}

func TestValidateAgainstSpecs_NestedObjectMismatch(t *testing.T) {
	specs := []fieldSpec{
		{
			Name: "meta",
			Kind: "object",
			Children: []fieldSpec{
				{Name: "host", Kind: "string"},
				{Name: "port", Kind: "number"},
			},
		},
	}
	parsed := map[string]any{
		"meta": map[string]any{
			"host": "server1",
			"port": "not-a-number", // wrong type
		},
	}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) == 0 {
		t.Error("expected nested type mismatch error, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "port") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error mentioning 'port', got %v", errs)
	}
}

func TestValidateAgainstSpecs_ListObjectMismatch(t *testing.T) {
	specs := []fieldSpec{
		{
			Name: "hosts",
			Kind: "list_object",
			Children: []fieldSpec{
				{Name: "name", Kind: "string"},
			},
		},
	}
	parsed := map[string]any{
		"hosts": []any{
			map[string]any{"name": "server1"},
			map[string]any{"name": float64(42)}, // wrong type
		},
	}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) == 0 {
		t.Error("expected element mismatch error, got none")
	}
}

func TestValidateAgainstSpecs_ListObjectEmptyPasses(t *testing.T) {
	specs := []fieldSpec{
		{
			Name: "hosts",
			Kind: "list_object",
			Children: []fieldSpec{
				{Name: "name", Kind: "string"},
			},
		},
	}
	parsed := map[string]any{"hosts": []any{}}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) != 0 {
		t.Errorf("expected empty list_object to pass, got %v", errs)
	}
}

func TestValidateAgainstSpecs_NotAnArray(t *testing.T) {
	specs := []fieldSpec{{Name: "tags", Kind: "list_string"}}
	parsed := map[string]any{"tags": "not-an-array"}
	errs := validateAgainstSpecs(parsed, specs)
	if len(errs) == 0 {
		t.Error("expected error when non-array given for list_string, got none")
	}
}
