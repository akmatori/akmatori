package api

import "encoding/json"

// Nullable distinguishes three JSON states for an optional field in a PATCH-style
// request body: absent (leave unchanged), explicit null (clear the value), and a
// concrete value (set it).
//
// A plain *T collapses the first two — encoding/json leaves the pointer nil for
// both — which makes it impossible to clear a nullable column back to NULL. The
// UnmarshalJSON method below is only invoked when the key is present in the
// payload, so Set reliably reports presence.
type Nullable[T any] struct {
	// Set is true when the request body contained the key at all.
	Set bool
	// Value is nil when the key was present but explicitly null.
	Value *T
}

// UnmarshalJSON records presence and decodes the value when non-null.
func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	n.Set = true
	if string(data) == "null" {
		n.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// MarshalJSON emits null for an unset or explicitly-null field.
func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if n.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(n.Value)
}
