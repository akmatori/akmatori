package api

import (
	"testing"
)

type testValidateStruct struct {
	Name     string `validate:"required,min=1,max=64"`
	Category string `validate:"omitempty,oneof=system custom monitoring"`
	Email    string `validate:"omitempty,email"`
	URL      string `validate:"omitempty,url"`
}

func TestValidate_ValidInput(t *testing.T) {
	s := testValidateStruct{
		Name:     "test-name",
		Category: "system",
	}
	errs := Validate(s)
	if errs != nil {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidate_MissingRequired(t *testing.T) {
	s := testValidateStruct{Name: ""}
	errs := Validate(s)
	if errs == nil {
		t.Fatal("expected validation errors")
	}
	if errs["name"] != "is required" {
		t.Errorf("name error = %q, want %q", errs["name"], "is required")
	}
}

func TestValidate_MaxLength(t *testing.T) {
	long := ""
	for i := 0; i < 65; i++ {
		long += "a"
	}
	s := testValidateStruct{Name: long}
	errs := Validate(s)
	if errs == nil {
		t.Fatal("expected validation errors")
	}
	if errs["name"] != "must be at most 64 characters" {
		t.Errorf("name error = %q, want %q", errs["name"], "must be at most 64 characters")
	}
}

func TestValidate_InvalidOneOf(t *testing.T) {
	s := testValidateStruct{Name: "test", Category: "invalid"}
	errs := Validate(s)
	if errs == nil {
		t.Fatal("expected validation errors")
	}
	if errs["category"] != "must be one of: system custom monitoring" {
		t.Errorf("category error = %q, want %q", errs["category"], "must be one of: system custom monitoring")
	}
}

func TestValidate_InvalidEmail(t *testing.T) {
	s := testValidateStruct{Name: "test", Email: "not-an-email"}
	errs := Validate(s)
	if errs == nil {
		t.Fatal("expected validation errors")
	}
	if errs["email"] != "must be a valid email" {
		t.Errorf("email error = %q, want %q", errs["email"], "must be a valid email")
	}
}

func TestValidate_InvalidURL(t *testing.T) {
	s := testValidateStruct{Name: "test", URL: "not-a-url"}
	errs := Validate(s)
	if errs == nil {
		t.Fatal("expected validation errors")
	}
	if errs["u_r_l"] != "must be a valid URL" {
		t.Errorf("url error = %q, want %q", errs["u_r_l"], "must be a valid URL")
	}
}

func TestValidate_MultipleFieldErrors(t *testing.T) {
	s := testValidateStruct{
		Name:     "",
		Category: "invalid",
		Email:    "not-an-email",
		URL:      "not-a-url",
	}
	errs := Validate(s)
	if errs == nil {
		t.Fatal("expected validation errors")
	}

	want := map[string]string{
		"name":     "is required",
		"category": "must be one of: system custom monitoring",
		"email":    "must be a valid email",
		"u_r_l":    "must be a valid URL",
	}
	for field, msg := range want {
		if errs[field] != msg {
			t.Errorf("%s error = %q, want %q", field, errs[field], msg)
		}
	}
	if len(errs) != len(want) {
		t.Errorf("error count = %d, want %d: %v", len(errs), len(want), errs)
	}
}

func TestValidate_NonStructInputReturnsFallbackError(t *testing.T) {
	errs := Validate(42)
	if errs == nil {
		t.Fatal("expected validation error")
	}
	if errs["_"] == "" {
		t.Errorf("fallback error = %q, want non-empty message", errs["_"])
	}
}

func TestValidate_OmitsEmptyOptional(t *testing.T) {
	s := testValidateStruct{Name: "test"}
	errs := Validate(s)
	if errs != nil {
		t.Errorf("expected no errors for empty optional fields, got %v", errs)
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Name", "name"},
		{"FirstName", "first_name"},
		{"APIKey", "a_p_i_key"},
		{"simple", "simple"},
		{"", ""},
	}

	for _, tt := range tests {
		got := toSnakeCase(tt.input)
		if got != tt.expected {
			t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
