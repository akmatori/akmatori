package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxBodySize is the maximum allowed request body size (1 MB).
const MaxBodySize = 1 << 20

// DecodeJSON reads and decodes a JSON request body into dst.
// It returns user-friendly error messages instead of leaking Go internals.
func DecodeJSON(r *http.Request, dst interface{}) error {
	if r.Body == nil {
		return errors.New("request body is empty")
	}

	// Enforce max body size.
	r.Body = http.MaxBytesReader(nil, r.Body, MaxBodySize)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err == nil {
		return nil
	}

	// Translate common JSON errors into friendly messages.
	var syntaxErr *json.SyntaxError
	var unmarshalTypeErr *json.UnmarshalTypeError
	var maxBytesErr *http.MaxBytesError

	switch {
	case errors.As(err, &syntaxErr):
		return fmt.Errorf("malformed JSON at position %d", syntaxErr.Offset)
	case errors.As(err, &unmarshalTypeErr):
		return fmt.Errorf("invalid value for field %q: expected %s", unmarshalTypeErr.Field, unmarshalTypeErr.Type)
	case errors.Is(err, io.EOF):
		return errors.New("request body is empty")
	case errors.As(err, &maxBytesErr):
		return fmt.Errorf("request body exceeds maximum size of %d bytes", MaxBodySize)
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return fmt.Errorf("unknown field %s", field)
	default:
		return errors.New("invalid JSON in request body")
	}
}
