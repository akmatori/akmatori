package services

import (
	"encoding/json"
	"os"
	"testing"
)

type secretRedactionCase struct {
	Name   string `json:"name"`
	Input  string `json:"input"`
	Output string `json:"output"`
	Count  int    `json:"count"`
}

// TestRedactSecrets_Fixture runs the cases shared with the gateway's
// memory.write (tests/fixtures/secret_redaction_cases.json).
func TestRedactSecrets_Fixture(t *testing.T) {
	b, err := os.ReadFile("../../tests/fixtures/secret_redaction_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []secretRedactionCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatalf("fixture has %d cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, n := RedactSecrets(c.Input)
			if got != c.Output {
				t.Errorf("output\n got: %q\nwant: %q", got, c.Output)
			}
			if n != c.Count {
				t.Errorf("count = %d, want %d", n, c.Count)
			}
		})
	}
}
