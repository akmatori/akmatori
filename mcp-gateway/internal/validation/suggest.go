package validation

import (
	"fmt"
	"strings"
)

// SuggestParam checks whether any key in args is a likely misspelling of requiredParam.
// It uses two heuristics:
//  1. Substring match: either string is contained in the other (case-insensitive).
//  2. Prefix match: the first three characters match (case-insensitive).
//
// Keys "tool_instance_id" and "logical_name" are always skipped because they are
// internal routing parameters, not user-facing tool parameters. An exact match
// (case-insensitive) is also skipped because the parameter exists with the right
// name — it is simply empty or the wrong type, not a naming mistake.
//
// Returns a human-readable suggestion string like
//
//	" (you passed 'lable' — did you mean 'label_name'?)"
//
// or an empty string when no similar key is found.
func SuggestParam(requiredParam string, args map[string]interface{}) string {
	requiredLower := strings.ToLower(requiredParam)

	for key := range args {
		// Skip internal routing parameters.
		if key == "tool_instance_id" || key == "logical_name" {
			continue
		}

		keyLower := strings.ToLower(key)

		// Exact match means the param name is correct — not a naming issue.
		if keyLower == requiredLower {
			continue
		}

		// Heuristic 1: substring containment in either direction.
		if strings.Contains(requiredLower, keyLower) || strings.Contains(keyLower, requiredLower) {
			return fmt.Sprintf(" (you passed '%s' — did you mean '%s'?)", key, requiredParam)
		}

		// Heuristic 2: shared 3-character prefix.
		if len(keyLower) >= 3 && len(requiredLower) >= 3 {
			if strings.HasPrefix(requiredLower, keyLower[:3]) || strings.HasPrefix(keyLower, requiredLower[:3]) {
				return fmt.Sprintf(" (you passed '%s' — did you mean '%s'?)", key, requiredParam)
			}
		}
	}

	return ""
}
