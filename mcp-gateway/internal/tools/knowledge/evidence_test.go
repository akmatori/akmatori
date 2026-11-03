package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/akmatori/mcp-gateway/internal/database"
)

// TestMemoryEvidenceFixture runs the agent evidence cases shared with the
// API (tests/fixtures/memory_evidence_cases.json).
func TestMemoryEvidenceFixture(t *testing.T) {
	b, err := os.ReadFile("../../../../tests/fixtures/memory_evidence_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Name   string            `json:"name"`
			Input  map[string]string `json:"input"`
			Expect struct {
				Evidence     string `json:"evidence"`
				Claim        string `json:"claim"`
				NoteContains string `json:"note_contains"`
				Truncated    bool   `json:"truncated"`
				NoteMaxBytes int    `json:"note_max_bytes"`
				Error        bool   `json:"error"`
			} `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 10 {
		t.Fatalf("fixture has %d cases", len(f.Cases))
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			in := memoryInput{
				Type: c.Input["type"], Description: c.Input["description"], Body: c.Input["body"],
				Evidence: c.Input["evidence"], Claim: c.Input["claim"], EvidenceNote: c.Input["evidence_note"],
			}
			truncated, err := applyAgentEvidence(&in)
			if c.Expect.Error {
				if err == nil {
					t.Fatalf("want error, got %+v", in)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if in.Evidence != c.Expect.Evidence || in.Claim != c.Expect.Claim {
				t.Errorf("evidence/claim = %q/%q, want %q/%q", in.Evidence, in.Claim, c.Expect.Evidence, c.Expect.Claim)
			}
			if c.Expect.NoteContains != "" && !strings.Contains(in.EvidenceNote, c.Expect.NoteContains) {
				t.Errorf("note = %q, want it to contain %q", in.EvidenceNote, c.Expect.NoteContains)
			}
			if truncated != c.Expect.Truncated {
				t.Errorf("truncated = %v", truncated)
			}
			if c.Expect.NoteMaxBytes > 0 && len(in.EvidenceNote) > c.Expect.NoteMaxBytes {
				t.Errorf("note is %d bytes", len(in.EvidenceNote))
			}
			if !utf8.ValidString(in.EvidenceNote) {
				t.Error("note is not valid UTF-8")
			}
		})
	}
}

func TestMemoryWrite_EvidenceStoredAndReturned(t *testing.T) {
	tool, db := newTestTool(t)
	ctx := runCtx("run-1")

	args := memArgs("ingest-gap", "Gap 2026-11-02.")
	args["description"] = "high load on ingest-1 caused the data gap"
	args["evidence"] = "observed"
	args["evidence_note"] = "load rose 10:05 UTC, gap started 10:02 UTC"
	r, err := write(t, tool, ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if r["evidence"] != "unverified" || r["claim"] != "causal" || !strings.Contains(r["evidence_note"].(string), "causal claim") {
		t.Errorf("response: %v", r)
	}
	var m database.Memory
	db.Where("name = ?", "ingest-gap").Take(&m)
	if m.Evidence != "unverified" || m.Claim != "causal" || !strings.HasPrefix(m.EvidenceNote, "load rose 10:05 UTC") {
		t.Errorf("stored: %+v", m)
	}

	// Reads return the grade.
	out, err := tool.MemoryGet(context.Background(), testScope, map[string]interface{}{"scope": "global", "name": "ingest-gap"})
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, out)
	if got["evidence"] != "unverified" || got["claim"] != "causal" || got["evidence_note"] == "" {
		t.Errorf("get: %v", got)
	}
	out, _ = tool.MemoryList(context.Background(), testScope, map[string]interface{}{"evidence": "unverified"})
	if l := decode(t, out); l["count"].(float64) != 1 {
		t.Errorf("list filter: %v", l)
	}
	out, _ = tool.MemoryList(context.Background(), testScope, map[string]interface{}{"evidence": "observed"})
	if l := decode(t, out); l["count"].(float64) != 0 {
		t.Errorf("list filter: %v", l)
	}
	if _, err := tool.MemoryList(context.Background(), testScope, map[string]interface{}{"evidence": "verified"}); err == nil {
		t.Error("an unknown evidence filter must fail")
	}
	out, _ = tool.MemorySearch(context.Background(), testScope, map[string]interface{}{"query": "ingest gap"})
	s := decode(t, out)
	if hits := s["results"].([]interface{}); len(hits) != 1 || hits[0].(map[string]interface{})["evidence"] != "unverified" {
		t.Errorf("search: %v", s)
	}
}

func TestMemoryWrite_ConfirmedRefused(t *testing.T) {
	tool, db := newTestTool(t)
	args := memArgs("redis-port", "16379")
	args["evidence"] = "confirmed"
	r, err := write(t, tool, runCtx("run-1"), args)
	if err != nil {
		t.Fatal(err)
	}
	if r["evidence"] != "unverified" {
		t.Errorf("response: %v", r)
	}
	var m database.Memory
	db.Where("name = ?", "redis-port").Take(&m)
	if m.Evidence != "unverified" || !strings.Contains(m.EvidenceNote, "only an operator") {
		t.Errorf("stored: %+v", m)
	}
}

func TestMemoryWrite_EvidenceOnlyChangeUpdates(t *testing.T) {
	tool, db := newTestTool(t)
	ctx := runCtx("run-1")
	if r, err := write(t, tool, ctx, memArgs("edge-nic", "v1")); err != nil || r["action"] != "created" || r["evidence"] != "observed" {
		t.Fatalf("create: %v %v", r, err)
	}
	args := memArgs("edge-nic", "v1")
	args["evidence_note"] = "kern.log 10:02"
	r, err := write(t, tool, ctx, args)
	if err != nil || r["action"] != "updated" {
		t.Fatalf("note only: %v %v", r, err)
	}
	var m database.Memory
	db.Where("name = ?", "edge-nic").Take(&m)
	if m.EvidenceNote != "kern.log 10:02" {
		t.Errorf("stored: %+v", m)
	}
	if r, _ := write(t, tool, ctx, args); r["action"] != "unchanged" {
		t.Errorf("same again: %v", r)
	}
}

func TestMemoryWrite_EvidenceArgTypes(t *testing.T) {
	tool, _ := newTestTool(t)
	for _, k := range []string{"evidence", "claim", "evidence_note"} {
		args := memArgs("x-"+strings.ReplaceAll(k, "_", "-"), "b")
		args[k] = 3
		if _, err := write(t, tool, runCtx("run-1"), args); err == nil {
			t.Errorf("%s=3 must fail", k)
		}
	}
	args := memArgs("null-ok", "b")
	args["evidence"] = nil
	if _, err := write(t, tool, runCtx("run-1"), args); err != nil {
		t.Errorf("null evidence counts as absent: %v", err)
	}
	args = memArgs("bad-claim", "b")
	args["claim"] = "maybe"
	if _, err := write(t, tool, runCtx("run-1"), args); err == nil {
		t.Error("unknown claim must fail")
	}
}

// An update that omits evidence does not raise an unverified memory, and
// an omitted note keeps the stored one. An explicit grade still applies.
func TestMemoryWrite_OmittedEvidenceKeepsStoredGrade(t *testing.T) {
	tool, db := newTestTool(t)
	db.Create(&database.Memory{Scope: "global", Type: "fact", Name: "legacy-row", Description: "d", Body: "old",
		CreatedBy: "agent", Evidence: "unverified", EvidenceNote: "legacy: not assessed"})
	r, err := write(t, tool, runCtx("run-1"), memArgs("legacy-row", "merged body"))
	if err != nil || r["action"] != "updated" || r["evidence"] != "unverified" {
		t.Fatalf("merge: %v %v", r, err)
	}
	var m database.Memory
	db.Where("name = ?", "legacy-row").Take(&m)
	// The text changed: the legacy note does not describe the rewrite.
	if m.Evidence != "unverified" || m.EvidenceNote != "" || m.Body != "merged body" {
		t.Errorf("stored: %+v", m)
	}
	// observed without a note is not a check: the row stays unverified.
	args := memArgs("legacy-row", "merged body")
	args["evidence"] = "observed"
	if r, err := write(t, tool, runCtx("run-1"), args); err != nil || r["evidence"] != "unverified" || r["note_on_evidence"] == nil {
		t.Fatalf("raise without note: %v %v", r, err)
	}
	db.Where("name = ?", "legacy-row").Take(&m)
	if m.Evidence != "unverified" || m.EvidenceNote != raiseNeedsNote {
		t.Errorf("raise without note stored: %+v", m)
	}
	args["evidence_note"] = "checked redis.conf 2026-11-03"
	if r, err := write(t, tool, runCtx("run-1"), args); err != nil || r["evidence"] != "observed" {
		t.Fatalf("explicit: %v %v", r, err)
	}
	db.Where("name = ?", "legacy-row").Take(&m)
	if m.Evidence != "observed" || m.EvidenceNote != "checked redis.conf 2026-11-03" {
		t.Errorf("explicit stored: %+v", m)
	}
}

// A refused write reports the stored grade, not the input's.
func TestMemoryWrite_RefusedReportsStoredEvidence(t *testing.T) {
	tool, db := newTestTool(t)
	db.Create(&database.Memory{Scope: "global", Type: "fact", Name: "op-fact", Description: "d", Body: "b", CreatedBy: "operator", Evidence: "confirmed", Claim: "factual"})
	args := memArgs("op-fact", "the gap was caused by load")
	r, err := write(t, tool, runCtx("run-1"), args)
	if err != nil || r["action"] != "refused_protected" {
		t.Fatalf("write: %v %v", r, err)
	}
	if r["evidence"] != "confirmed" || r["claim"] != "factual" || r["note_on_evidence"] != nil {
		t.Errorf("response: %v", r)
	}
}

// A confirmed row that an agent may still write (legacy authorship) loses
// the confirmation when the text changes, with a note.
func TestMemoryWrite_ChangedTextResetsConfirmed(t *testing.T) {
	tool, db := newTestTool(t)
	db.Create(&database.Memory{Scope: "global", Type: "fact", Name: "legacy-confirmed", Description: "desc legacy-confirmed", Body: "b", Evidence: "confirmed", Claim: "factual", EvidenceNote: "checked by ops"})
	r, err := write(t, tool, runCtx("run-1"), memArgs("legacy-confirmed", "b"))
	if err != nil || r["action"] != "unchanged" || r["evidence"] != "confirmed" {
		t.Fatalf("same text: %v %v", r, err)
	}
	r, err = write(t, tool, runCtx("run-1"), memArgs("legacy-confirmed", "b2"))
	if err != nil || r["action"] != "updated" || r["evidence"] != "observed" {
		t.Fatalf("changed text: %v %v", r, err)
	}
	var m database.Memory
	db.Where("name = ?", "legacy-confirmed").Take(&m)
	if m.Evidence != "observed" || m.EvidenceNote != confirmationResetNote {
		t.Errorf("stored: %+v", m)
	}
}
