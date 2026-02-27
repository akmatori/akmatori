package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestDecodeJSON_ValidInput(t *testing.T) {
	body := `{"name":"test","value":42}`
	r := newRequest(body)

	var dst struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	if err := DecodeJSON(r, &dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst.Name != "test" {
		t.Errorf("name = %q, want %q", dst.Name, "test")
	}
	if dst.Value != 42 {
		t.Errorf("value = %d, want %d", dst.Value, 42)
	}
}

func TestDecodeJSON_NilBody(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "/test", nil)

	var dst struct{}
	err := DecodeJSON(r, &dst)
	if err == nil {
		t.Fatal("expected error for nil body")
	}
	if err.Error() != "request body is empty" {
		t.Errorf("error = %q, want %q", err.Error(), "request body is empty")
	}
}

func TestDecodeJSON_EmptyBody(t *testing.T) {
	r := newRequest("")

	var dst struct{}
	err := DecodeJSON(r, &dst)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
	if err.Error() != "request body is empty" {
		t.Errorf("error = %q, want %q", err.Error(), "request body is empty")
	}
}

func TestDecodeJSON_MalformedJSON(t *testing.T) {
	r := newRequest(`{invalid}`)

	var dst struct{}
	err := DecodeJSON(r, &dst)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "malformed JSON") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "malformed JSON")
	}
}

func TestDecodeJSON_TypeMismatch(t *testing.T) {
	r := newRequest(`{"value":"not_a_number"}`)

	var dst struct {
		Value int `json:"value"`
	}
	err := DecodeJSON(r, &dst)
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}
	if !strings.Contains(err.Error(), "invalid value") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid value")
	}
}

func TestDecodeJSON_UnknownField(t *testing.T) {
	r := newRequest(`{"name":"test","extra":"field"}`)

	var dst struct {
		Name string `json:"name"`
	}
	err := DecodeJSON(r, &dst)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "unknown field")
	}
}

func TestDecodeJSON_OversizedBody(t *testing.T) {
	// Create a body that exceeds MaxBodySize (1MB)
	huge := `{"data":"` + strings.Repeat("x", MaxBodySize+1) + `"}`
	r := newRequest(huge)

	var dst struct {
		Data string `json:"data"`
	}
	err := DecodeJSON(r, &dst)
	if err == nil {
		t.Fatal("expected error for oversized body")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "exceeds maximum size")
	}
}

// newRequest creates an http.Request with the given JSON body.
func newRequest(body string) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}
