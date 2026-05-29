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
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %T", keyTok)
		}
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
		return fieldSpec{Name: name, Kind: "string"}, nil
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

	// Peek at first element to determine element type.
	tok, err := dec.Token()
	if err != nil {
		return fieldSpec{}, err
	}

	var spec fieldSpec
	spec.Name = name

	switch tok.(type) {
	case json.Delim:
		d := tok.(json.Delim)
		if d == '{' {
			// list-of-objects: use first element's schema for children.
			children, err := inferObjectFields(dec)
			if err != nil {
				return fieldSpec{}, err
			}
			spec.Kind = "list_object"
			spec.Children = children
		} else if d == '[' {
			// Array-of-arrays: skip inner array, treat as list_string.
			if err := skipUntilArrayClose(dec); err != nil {
				return fieldSpec{}, err
			}
			spec.Kind = "list_string"
		} else {
			return fieldSpec{}, fmt.Errorf("unexpected delimiter %q in array %q", d, name)
		}
	case string:
		spec.Kind = "list_string"
	case float64:
		spec.Kind = "list_number"
	case bool:
		spec.Kind = "list_bool"
	case nil:
		spec.Kind = "list_string"
	}

	// Skip any remaining elements and consume the closing ']'.
	if err := skipUntilArrayClose(dec); err != nil {
		return fieldSpec{}, err
	}
	return spec, nil
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
