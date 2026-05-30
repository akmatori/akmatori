package output

import (
	"fmt"
	"strconv"
	"strings"
)

// FieldSpec describes a single field in the expected LLM output schema.
// Kind is one of: "string", "number", "bool", "list_string", "list_number",
// "list_object", "object". Children is populated for "object" and "list_object".
// Enum, when non-nil, restricts "string" fields to the listed values (case-insensitive).
// NonEmpty, when true, rejects blank strings (after TrimSpace) in addition to the
// type check — used to restore the old mandatory-summary constraint on the default schema.
type FieldSpec struct {
	Name     string
	Kind     string
	Children []FieldSpec
	Enum     []string
	NonEmpty bool
}

// RenderForSlack renders a parsed LLM response map as Slack mrkdwn text.
// It walks specs in order, producing a formatted section per field.
// Returns empty string when nothing produces visible output.
func RenderForSlack(parsed map[string]any, specs []FieldSpec) string {
	var sb strings.Builder
	for _, spec := range specs {
		val, ok := parsed[spec.Name]
		if !ok {
			continue
		}
		sb.WriteString(renderField(spec, val))
	}
	return sb.String()
}

// renderField renders a single field value according to its spec kind.
func renderField(spec FieldSpec, val any) string {
	heading := titleCase(spec.Name)
	switch spec.Kind {
	case "string":
		s, ok := val.(string)
		if !ok {
			return ""
		}
		if spec.Name == "status" {
			lower := strings.ToLower(s)
			if lower == "resolved" || lower == "unresolved" || lower == "escalate" {
				return fmt.Sprintf("*%s:* %s %s\n", heading, getStatusEmoji(lower), s)
			}
		}
		return fmt.Sprintf("*%s:* %s\n", heading, s)
	case "number":
		return fmt.Sprintf("*%s:* %s\n", heading, formatNumber(val))
	case "bool":
		return fmt.Sprintf("*%s:* %v\n", heading, val)
	case "list_string", "list_number", "list_bool":
		arr, ok := val.([]any)
		if !ok || len(arr) == 0 {
			return ""
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("*%s:*\n", heading))
		for _, elem := range arr {
			if spec.Kind == "list_number" {
				sb.WriteString(fmt.Sprintf(" • %s\n", formatNumber(elem)))
			} else {
				sb.WriteString(fmt.Sprintf(" • %v\n", elem))
			}
		}
		return sb.String()
	case "list_object":
		arr, ok := val.([]any)
		if !ok || len(arr) == 0 {
			return ""
		}
		var bullets strings.Builder
		for _, elem := range arr {
			obj, ok := elem.(map[string]any)
			if !ok {
				continue
			}
			var parts []string
			for _, child := range spec.Children {
				cv, ok := obj[child.Name]
				if !ok {
					continue
				}
				switch child.Kind {
				case "string", "bool":
					parts = append(parts, fmt.Sprintf("%s: %v", titleCase(child.Name), cv))
				case "number":
					parts = append(parts, fmt.Sprintf("%s: %s", titleCase(child.Name), formatNumber(cv)))
				default:
					if rendered := renderField(child, cv); rendered != "" {
						parts = append(parts, strings.TrimRight(rendered, "\n"))
					}
				}
			}
			if len(parts) > 0 {
				bullets.WriteString(fmt.Sprintf(" • %s\n", strings.Join(parts, " | ")))
			}
		}
		if bullets.Len() == 0 {
			return ""
		}
		return fmt.Sprintf("*%s:*\n", heading) + bullets.String()
	case "object":
		obj, ok := val.(map[string]any)
		if !ok {
			return ""
		}
		var children strings.Builder
		for _, child := range spec.Children {
			cv, ok := obj[child.Name]
			if !ok {
				continue
			}
			switch child.Kind {
			case "string", "bool":
				children.WriteString(fmt.Sprintf(" • %s: %v\n", titleCase(child.Name), cv))
			case "number":
				children.WriteString(fmt.Sprintf(" • %s: %s\n", titleCase(child.Name), formatNumber(cv)))
			default:
				if rendered := renderField(child, cv); rendered != "" {
					children.WriteString(rendered)
				}
			}
		}
		if children.Len() == 0 {
			return ""
		}
		return fmt.Sprintf("*%s:*\n", heading) + children.String()
	}
	return ""
}

// formatNumber formats a float64 without scientific notation for clean Slack display.
func formatNumber(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", v)
}

// titleCase converts a snake_case or kebab-case identifier to Title Case.
func titleCase(key string) string {
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return key
	}
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
