package api

import (
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate = validator.New(validator.WithRequiredStructEnabled())

// Validate validates a struct using go-playground/validator tags.
// Returns nil on success or a map of field-name → error-message.
func Validate(s interface{}) map[string]string {
	err := validate.Struct(s)
	if err == nil {
		return nil
	}

	validationErrors, ok := err.(validator.ValidationErrors)
	if !ok {
		return map[string]string{"_": err.Error()}
	}

	errs := make(map[string]string, len(validationErrors))
	for _, fe := range validationErrors {
		field := toSnakeCase(fe.Field())
		errs[field] = validationMessage(fe)
	}
	return errs
}

// validationMessage returns a human-readable message for a validation error.
func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "min":
		return fmt.Sprintf("must be at least %s characters", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s characters", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of: %s", fe.Param())
	case "url":
		return "must be a valid URL"
	case "email":
		return "must be a valid email"
	default:
		return fmt.Sprintf("failed %s validation", fe.Tag())
	}
}

// toSnakeCase converts a CamelCase field name to snake_case.
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(r + 32) // lowercase
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
