package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/akmatori/akmatori/internal/output"
)

// defaultSchemaExample is the built-in four-key output shape used when an
// operator has not configured a custom OutputSchemaExample.
const defaultSchemaExample = `{"status":"resolved","summary":"1-3 sentence description of what happened and how it was resolved.","actions_taken":["action 1"],"recommendations":["recommendation 1"]}`

// fieldSpec is a package-local alias for output.FieldSpec so internal helpers
// can be written concisely without repeating the package qualifier.
type fieldSpec = output.FieldSpec

// ValidateSchemaExample checks that example is a well-formed schema string that
// inferSchema can process at format time. An empty string is accepted (the built-in
// default is used at runtime). Returns a descriptive error when the example contains
// null fields, mixed-type arrays, or other unsupported constructs.
func ValidateSchemaExample(example string) error {
	if example == "" {
		return nil
	}
	_, err := inferSchema(example)
	return err
}

// inferSchema parses the example JSON string and returns an ordered slice of
// fieldSpecs. Key order is preserved via json.Decoder token walk. Returns an
// error when example is invalid JSON or the top-level value is not an object.
func inferSchema(example string) ([]fieldSpec, error) {
	dec := json.NewDecoder(strings.NewReader(example))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("top-level JSON value must be an object")
	}
	return inferObjectFields(dec)
}

// inferObjectFields reads key-value pairs from dec until the matching '}'.
// The opening '{' must already have been consumed.
func inferObjectFields(dec *json.Decoder) ([]fieldSpec, error) {
	var specs []fieldSpec
	seen := make(map[string]bool)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %T", keyTok)
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate key %q: each key must appear exactly once in the example object", key)
		}
		seen[key] = true
		spec, err := inferValue(dec, key)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	if _, err := dec.Token(); err != nil { // consume '}'
		return nil, err
	}
	return specs, nil
}

// inferValue reads one JSON value from dec and returns a fieldSpec named name.
func inferValue(dec *json.Decoder, name string) (fieldSpec, error) {
	tok, err := dec.Token()
	if err != nil {
		return fieldSpec{}, err
	}
	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			children, err := inferObjectFields(dec)
			if err != nil {
				return fieldSpec{}, err
			}
			return fieldSpec{Name: name, Kind: "object", Children: children}, nil
		}
		if v == '[' {
			return inferArray(dec, name)
		}
		return fieldSpec{}, fmt.Errorf("unexpected delimiter %q for key %q", v, name)
	case string:
		return fieldSpec{Name: name, Kind: "string"}, nil
	case float64:
		return fieldSpec{Name: name, Kind: "number"}, nil
	case bool:
		return fieldSpec{Name: name, Kind: "bool"}, nil
	case nil:
		return fieldSpec{}, fmt.Errorf("null value for key %q: null is not a supported field type; use a string, number, bool, array, or object instead", name)
	default:
		return fieldSpec{}, fmt.Errorf("unexpected token type %T for key %q", tok, name)
	}
}

// inferArray infers the kind of an array field. The opening '[' must already
// have been consumed. Empty arrays default to list_string.
func inferArray(dec *json.Decoder, name string) (fieldSpec, error) {
	if !dec.More() {
		if _, err := dec.Token(); err != nil { // consume ']'
			return fieldSpec{}, err
		}
		return fieldSpec{Name: name, Kind: "list_string"}, nil
	}

	// Read first element to determine element type.
	tok, err := dec.Token()
	if err != nil {
		return fieldSpec{}, err
	}

	var spec fieldSpec
	spec.Name = name

	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			// list-of-objects: use first element's schema for children.
			children, err := inferObjectFields(dec)
			if err != nil {
				return fieldSpec{}, err
			}
			spec.Kind = "list_object"
			spec.Children = children
		} else if v == '[' {
			return fieldSpec{}, fmt.Errorf("array-of-arrays not supported for key %q: use strings, numbers, bools, or objects as array elements", name)
		} else {
			return fieldSpec{}, fmt.Errorf("unexpected delimiter %q in array %q", v, name)
		}
	case string:
		spec.Kind = "list_string"
	case float64:
		spec.Kind = "list_number"
	case bool:
		spec.Kind = "list_bool"
	case nil:
		return fieldSpec{}, fmt.Errorf("null element in array %q: null is not a supported element type; use strings, numbers, bools, or objects instead", name)
	default:
		return fieldSpec{}, fmt.Errorf("unexpected token type %T in array %q", v, name)
	}

	// Validate remaining elements: reject null and mixed-type arrays.
	for dec.More() {
		nextTok, err := dec.Token()
		if err != nil {
			return fieldSpec{}, err
		}
		switch v := nextTok.(type) {
		case nil:
			return fieldSpec{}, fmt.Errorf("null element in array %q: null is not a supported element type", name)
		case json.Delim:
			if v == '{' {
				if spec.Kind != "list_object" {
					return fieldSpec{}, fmt.Errorf("mixed element types in array %q: expected %s elements, got object", name, spec.Kind)
				}
				// Parse the later object body to catch null fields and type inconsistencies.
				laterChildren, err := inferObjectFields(dec)
				if err != nil {
					return fieldSpec{}, fmt.Errorf("invalid element in array %q: %w", name, err)
				}
				if err := checkLaterObjectCompatibility(spec.Children, laterChildren, name); err != nil {
					return fieldSpec{}, err
				}
			} else if v == '[' {
				return fieldSpec{}, fmt.Errorf("mixed element types in array %q: expected %s elements, got array", name, spec.Kind)
			} else {
				return fieldSpec{}, fmt.Errorf("unexpected delimiter %q in array %q", v, name)
			}
		case string:
			if spec.Kind != "list_string" {
				return fieldSpec{}, fmt.Errorf("mixed element types in array %q: expected %s elements, got string", name, spec.Kind)
			}
		case float64:
			if spec.Kind != "list_number" {
				return fieldSpec{}, fmt.Errorf("mixed element types in array %q: expected %s elements, got number", name, spec.Kind)
			}
		case bool:
			if spec.Kind != "list_bool" {
				return fieldSpec{}, fmt.Errorf("mixed element types in array %q: expected %s elements, got bool", name, spec.Kind)
			}
		}
	}

	if _, err := dec.Token(); err != nil { // consume ']'
		return fieldSpec{}, err
	}
	return spec, nil
}

// checkLaterObjectCompatibility verifies that a later list_object element is
// compatible with the schema inferred from the first element. It checks that
// every key from firstChildren is present in laterChildren with the same Kind,
// and recurses into nested object and list_object children.
func checkLaterObjectCompatibility(firstChildren, laterChildren []fieldSpec, arrayField string) error {
	for _, fc := range firstChildren {
		found := false
		for _, lc := range laterChildren {
			if lc.Name == fc.Name {
				found = true
				if lc.Kind != fc.Kind {
					return fmt.Errorf("inconsistent types in array %q: field %q is %s in first element but %s in a later element", arrayField, fc.Name, fc.Kind, lc.Kind)
				}
				if (fc.Kind == "object" || fc.Kind == "list_object") && len(fc.Children) > 0 {
					if err := checkLaterObjectCompatibility(fc.Children, lc.Children, arrayField); err != nil {
						return err
					}
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("inconsistent shapes in array %q: field %q present in first element but missing in a later element", arrayField, fc.Name)
		}
	}
	return nil
}

// skipUntilArrayClose consumes tokens until the matching ']' is reached,
// tracking depth for any nested objects or arrays within the elements.
func skipUntilArrayClose(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				if depth == 0 {
					return nil
				}
				depth--
			}
		}
	}
}

// buildSchemaInstruction returns the JSON schema instruction to append to the
// system prompt. It pretty-prints the operator example (preserving key order)
// and wraps it in the "Return ONLY a single JSON object" directive.
func buildSchemaInstruction(example string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(example), "", "  "); err != nil {
		return "\n\nReturn ONLY a single JSON object — no markdown fences, no preamble, no trailing text — matching exactly this shape:\n" + example
	}
	return "\n\nReturn ONLY a single JSON object — no markdown fences, no preamble, no trailing text — matching exactly this shape:\n" + buf.String()
}

// validateAgainstSpecs checks that parsed contains all keys required by specs,
// each with the correct type. Extra keys in parsed are tolerated. Returns
// human-readable error strings; an empty slice means validation passed.
func validateAgainstSpecs(parsed map[string]any, specs []fieldSpec) []string {
	var errs []string
	for _, spec := range specs {
		val, ok := parsed[spec.Name]
		if !ok {
			errs = append(errs, fmt.Sprintf("missing required key %q", spec.Name))
			continue
		}
		errs = append(errs, checkKind(spec, val)...)
	}
	return errs
}

// checkKind validates that val matches the Kind declared in spec.
func checkKind(spec fieldSpec, val any) []string {
	var errs []string
	switch spec.Kind {
	case "string":
		s, ok := val.(string)
		if !ok {
			errs = append(errs, fmt.Sprintf("key %q: expected string, got %T", spec.Name, val))
			break
		}
		if spec.NonEmpty && strings.TrimSpace(s) == "" {
			errs = append(errs, fmt.Sprintf("key %q: must be a non-empty string", spec.Name))
		}
		if len(spec.Enum) > 0 {
			found := false
			for _, e := range spec.Enum {
				if strings.EqualFold(s, e) {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Sprintf("key %q: expected one of %v, got %q", spec.Name, spec.Enum, s))
			}
		}
	case "number":
		if _, ok := val.(float64); !ok {
			errs = append(errs, fmt.Sprintf("key %q: expected number, got %T", spec.Name, val))
		}
	case "bool":
		if _, ok := val.(bool); !ok {
			errs = append(errs, fmt.Sprintf("key %q: expected bool, got %T", spec.Name, val))
		}
	case "list_string":
		arr, ok := val.([]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("key %q: expected array, got %T", spec.Name, val))
			break
		}
		for i, elem := range arr {
			if _, ok := elem.(string); !ok {
				errs = append(errs, fmt.Sprintf("key %q: element %d expected string, got %T", spec.Name, i, elem))
			}
		}
	case "list_number":
		arr, ok := val.([]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("key %q: expected array, got %T", spec.Name, val))
			break
		}
		for i, elem := range arr {
			if _, ok := elem.(float64); !ok {
				errs = append(errs, fmt.Sprintf("key %q: element %d expected number, got %T", spec.Name, i, elem))
			}
		}
	case "list_bool":
		arr, ok := val.([]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("key %q: expected array, got %T", spec.Name, val))
			break
		}
		for i, elem := range arr {
			if _, ok := elem.(bool); !ok {
				errs = append(errs, fmt.Sprintf("key %q: element %d expected bool, got %T", spec.Name, i, elem))
			}
		}
	case "list_object":
		arr, ok := val.([]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("key %q: expected array, got %T", spec.Name, val))
			break
		}
		for i, elem := range arr {
			obj, ok := elem.(map[string]any)
			if !ok {
				errs = append(errs, fmt.Sprintf("key %q[%d]: expected object, got %T", spec.Name, i, elem))
				continue
			}
			for _, ce := range validateAgainstSpecs(obj, spec.Children) {
				errs = append(errs, fmt.Sprintf("key %q[%d]: %s", spec.Name, i, ce))
			}
		}
	case "object":
		obj, ok := val.(map[string]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("key %q: expected object, got %T", spec.Name, val))
			break
		}
		for _, ce := range validateAgainstSpecs(obj, spec.Children) {
			errs = append(errs, fmt.Sprintf("key %q.%s", spec.Name, ce))
		}
	}
	return errs
}
