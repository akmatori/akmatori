package handlers

import (
	"errors"
	"testing"
)

func TestToolConflictMessage(t *testing.T) {
	const name = "A tool with this name already exists. Pick another name or edit the existing tool."
	const logical = "A tool with this logical name already exists. Pick another name or edit the existing tool."
	cases := map[string]string{
		`failed to create tool instance: ERROR: duplicate key value violates unique constraint "idx_tool_instances_name" (SQLSTATE 23505)`: name,
		`UNIQUE constraint failed: tool_instances.name`:                                                            name,
		`ERROR: duplicate key value violates unique constraint "idx_tool_instances_logical_name" (SQLSTATE 23505)`: logical,
		`tool instance with logical name "x" already exists`:                                                       logical,
		`tool type already exists`:                                                                                 "tool type already exists",
	}
	for in, want := range cases {
		if got := toolConflictMessage(errors.New(in)); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
