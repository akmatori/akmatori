package services

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/akmatori/akmatori/internal/database"
)

type evidenceFixtureCase struct {
	Name  string            `json:"name"`
	Input map[string]string `json:"input"`
	// Expect: evidence, claim, note_contains, truncated, note_max_bytes, error.
	Expect struct {
		Evidence     string `json:"evidence"`
		Claim        string `json:"claim"`
		NoteContains string `json:"note_contains"`
		Truncated    bool   `json:"truncated"`
		NoteMaxBytes int    `json:"note_max_bytes"`
		Error        bool   `json:"error"`
	} `json:"expect"`
}

// TestMemoryEvidenceFixture runs the agent evidence cases shared with the
// gateway's memory.write (tests/fixtures/memory_evidence_cases.json).
func TestMemoryEvidenceFixture(t *testing.T) {
	b, err := os.ReadFile("../../tests/fixtures/memory_evidence_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []evidenceFixtureCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 10 {
		t.Fatalf("fixture has %d cases", len(f.Cases))
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			m := &database.Memory{
				Type: c.Input["type"], Description: c.Input["description"], Body: c.Input["body"],
				Evidence: c.Input["evidence"], Claim: c.Input["claim"], EvidenceNote: c.Input["evidence_note"],
			}
			truncated, err := ApplyAgentMemoryEvidence(m)
			if c.Expect.Error {
				if err == nil {
					t.Fatalf("want error, got %+v", m)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.Evidence != c.Expect.Evidence || m.Claim != c.Expect.Claim {
				t.Errorf("evidence/claim = %q/%q, want %q/%q", m.Evidence, m.Claim, c.Expect.Evidence, c.Expect.Claim)
			}
			if c.Expect.NoteContains != "" && !strings.Contains(m.EvidenceNote, c.Expect.NoteContains) {
				t.Errorf("note = %q, want it to contain %q", m.EvidenceNote, c.Expect.NoteContains)
			}
			if truncated != c.Expect.Truncated {
				t.Errorf("truncated = %v", truncated)
			}
			if c.Expect.NoteMaxBytes > 0 && len(m.EvidenceNote) > c.Expect.NoteMaxBytes {
				t.Errorf("note is %d bytes", len(m.EvidenceNote))
			}
			if !utf8.ValidString(m.EvidenceNote) {
				t.Error("note is not valid UTF-8")
			}
		})
	}
}

// Operator writes may set any grade; an empty one is filled.
func TestValidateMemoryEvidence_Operator(t *testing.T) {
	svc := &MemoryService{}
	cases := []struct {
		in, claim, want string
		wantErr         bool
	}{
		{"", "", MemoryEvidenceObserved, false},
		{"", MemoryClaimCausal, MemoryEvidenceUnverified, false},
		{MemoryEvidenceConfirmed, MemoryClaimCausal, MemoryEvidenceConfirmed, false},
		{"verified", "", "", true},
	}
	for _, c := range cases {
		m := validMemory("op-" + c.want)
		m.Evidence, m.Claim, m.CreatedBy = c.in, c.claim, MemoryCreatedByOperator
		err := svc.validate(m)
		if (err != nil) != c.wantErr {
			t.Fatalf("%+v: err=%v", c, err)
		}
		if err == nil && m.Evidence != c.want {
			t.Errorf("%+v: evidence = %q", c, m.Evidence)
		}
	}
	m := validMemory("long-note")
	m.EvidenceNote = strings.Repeat("x", MemoryEvidenceNoteMaxLen+1)
	if err := svc.validate(m); err == nil {
		t.Error("a note over the cap must fail for operator writes")
	}
}

// The upsert writes the evidence fields and counts a change in them as a
// change, so updated_at moves; equal fields leave the row alone.
func TestUpsertByName_EvidenceChange(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	m := validMemory("edge-nic")
	m.CreatedBy = MemoryCreatedByAgent
	saved, err := svc.UpsertByName(m)
	if err != nil || saved.Evidence != MemoryEvidenceObserved {
		t.Fatalf("create: %+v %v", saved, err)
	}
	var first database.Memory
	svc.db.Where("name = ?", "edge-nic").Take(&first)

	same := validMemory("edge-nic")
	same.CreatedBy = MemoryCreatedByAgent
	if _, err := svc.UpsertByName(same); err != nil {
		t.Fatal(err)
	}
	var again database.Memory
	svc.db.Where("name = ?", "edge-nic").Take(&again)
	if !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Error("an equal upsert moved updated_at")
	}

	// Only the note changes: empty → value.
	noted := validMemory("edge-nic")
	noted.CreatedBy = MemoryCreatedByAgent
	noted.EvidenceNote = "kern.log 10:02"
	if _, err := svc.UpsertByName(noted); err != nil {
		t.Fatal(err)
	}
	var got database.Memory
	svc.db.Where("name = ?", "edge-nic").Take(&got)
	if got.EvidenceNote != "kern.log 10:02" {
		t.Errorf("note not written: %+v", got)
	}

	// Only the grade changes.
	graded := validMemory("edge-nic")
	graded.CreatedBy = MemoryCreatedByAgent
	graded.EvidenceNote = "kern.log 10:02"
	graded.Evidence = MemoryEvidenceUnverified
	if _, err := svc.UpsertByName(graded); err != nil {
		t.Fatal(err)
	}
	svc.db.Where("name = ?", "edge-nic").Take(&got)
	if got.Evidence != MemoryEvidenceUnverified {
		t.Errorf("grade not written: %+v", got)
	}
}

func TestUpdateMemory_EvidenceFields(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	m := validMemory("lb-cert")
	m.EvidenceNote = "seen in openssl output"
	created, err := svc.CreateMemory(m)
	if err != nil {
		t.Fatal(err)
	}
	// The operator confirms; the note stays (omitted = unchanged).
	updated, err := svc.UpdateMemory(created.ID, &database.Memory{Evidence: MemoryEvidenceConfirmed, Claim: MemoryClaimCausal}, MemoryUpdateSet{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Evidence != MemoryEvidenceConfirmed || updated.Claim != MemoryClaimCausal || updated.EvidenceNote != "seen in openssl output" {
		t.Errorf("update: %+v", updated)
	}
	// An explicit empty note clears it.
	cleared, err := svc.UpdateMemory(created.ID, &database.Memory{}, MemoryUpdateSet{EvidenceNote: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.EvidenceNote != "" || cleared.Evidence != MemoryEvidenceConfirmed {
		t.Errorf("clear note: %+v", cleared)
	}
	if _, err := svc.UpdateMemory(created.ID, &database.Memory{Evidence: "verified"}, MemoryUpdateSet{}); err == nil {
		t.Error("an unknown grade must fail")
	}
}

func TestListMemories_EvidenceFilter(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	a := validMemory("a-fact")
	b := validMemory("b-cause")
	b.Claim = MemoryClaimCausal
	for _, m := range []*database.Memory{a, b} {
		if _, err := svc.CreateMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.ListMemories(MemoryFilter{Evidence: MemoryEvidenceUnverified})
	if err != nil || len(got) != 1 || got[0].Name != "b-cause" {
		t.Fatalf("filter: %+v %v", got, err)
	}
}

// The manifest shows the grade, and the file round-trip keeps it.
func TestManifestAndFileCarryEvidence(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	m := validMemory("ingest-gap")
	m.Claim = MemoryClaimCausal
	m.EvidenceNote = "load rose 10:05, gap 10:02"
	if _, err := svc.CreateMemory(m); err != nil {
		t.Fatal(err)
	}
	manifest := RenderMemoryManifest(svc.db, MemoryScopeGlobal)
	if !strings.Contains(manifest, "| name | type | evidence | description |") || !strings.Contains(manifest, "| `ingest-gap` | fact | unverified | ") {
		t.Errorf("manifest:\n%s", manifest)
	}
	var stored database.Memory
	svc.db.Where("name = ?", "ingest-gap").Take(&stored)
	parsed, _, err := parseMemoryFile([]byte(renderMemoryFile(stored)), MemoryScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Evidence != MemoryEvidenceUnverified || parsed.Claim != MemoryClaimCausal || parsed.EvidenceNote != "load rose 10:05, gap 10:02" {
		t.Errorf("round-trip: %+v", parsed)
	}
}

// A context-file summary restates the file: observed, factual, on create
// and on rerun.
func TestSaveFileSummary_Observed(t *testing.T) {
	svc := setupMemoryServiceTest(t)
	m, err := svc.SaveFileSummary(3, "runbook.pdf", "Runbook for the edge", "The edge fails over because of BGP.")
	if err != nil {
		t.Fatal(err)
	}
	if m.Evidence != MemoryEvidenceObserved || m.Claim != MemoryClaimFactual {
		t.Errorf("create: %+v", m)
	}
	svc.db.Model(&database.Memory{}).Where("id = ?", m.ID).Update("evidence", MemoryEvidenceUnverified)
	again, err := svc.SaveFileSummary(3, "runbook.pdf", "Runbook for the edge v2", "b")
	if err != nil {
		t.Fatal(err)
	}
	if again.Evidence != MemoryEvidenceObserved {
		t.Errorf("rerun: %+v", again)
	}
}

// A rewrite is not a check: the stored grade rules the merge.
func TestMergeStoredMemoryEvidence(t *testing.T) {
	cases := []struct {
		name                 string
		stored, in           database.Memory
		omitted              bool
		wantEvidence, wantNo string
		wantRefused          bool
	}{
		{"omitted, same text keeps confirmed", database.Memory{Description: "d", Body: "b", Evidence: "confirmed", Claim: "causal", EvidenceNote: "checked by ops"}, database.Memory{Description: "d", Body: "b", Evidence: "observed", Claim: "factual"}, true, "confirmed", "checked by ops", false},
		{"omitted, changed text resets confirmed", database.Memory{Description: "d", Body: "b", Evidence: "confirmed"}, database.Memory{Description: "d", Body: "b2", Evidence: "observed", Claim: "factual"}, true, "observed", memoryConfirmationResetNote, false},
		{"omitted, changed text keeps unverified and drops the legacy note", database.Memory{Description: "d", Body: "b", Evidence: "unverified", EvidenceNote: MemoryLegacyEvidenceNote}, database.Memory{Description: "d", Body: "b2", Evidence: "observed", Claim: "factual"}, true, "unverified", "", false},
		{"omitted, changed text keeps an operator note", database.Memory{Description: "d", Body: "b", Evidence: "unverified", EvidenceNote: "suspected, check X"}, database.Memory{Description: "d", Body: "b2", Evidence: "observed", Claim: "factual"}, true, "unverified", "suspected, check X", false},
		{"observed without a note does not raise", database.Memory{Description: "d", Body: "b", Evidence: "unverified", EvidenceNote: MemoryLegacyEvidenceNote}, database.Memory{Description: "d", Body: "b2", Evidence: "observed", Claim: "factual"}, false, "unverified", memoryRaiseNeedsNote, true},
		{"observed with a note raises", database.Memory{Description: "d", Body: "b", Evidence: "unverified", EvidenceNote: MemoryLegacyEvidenceNote}, database.Memory{Description: "d", Body: "b2", Evidence: "observed", Claim: "factual", EvidenceNote: "seen in redis.conf"}, false, "observed", "seen in redis.conf", false},
		{"omitted on observed row follows the text", database.Memory{Description: "d", Body: "b", Evidence: "observed", Claim: "factual"}, database.Memory{Description: "d", Body: "b2", Evidence: "unverified", Claim: "causal", EvidenceNote: "causal claim; stored as unverified"}, true, "unverified", "causal claim; stored as unverified", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeStoredMemoryEvidence(&c.in, &c.stored, c.omitted)
			if got != c.wantRefused || c.in.Evidence != c.wantEvidence || c.in.EvidenceNote != c.wantNo {
				t.Errorf("refused=%v evidence=%q note=%q", got, c.in.Evidence, c.in.EvidenceNote)
			}
		})
	}
	// Same text: the stored claim wins too.
	in := database.Memory{Description: "d", Body: "b", Evidence: "observed", Claim: "factual"}
	MergeStoredMemoryEvidence(&in, &database.Memory{Description: "d", Body: "b", Evidence: "unverified", Claim: "causal"}, true)
	if in.Claim != "causal" || in.Evidence != "unverified" {
		t.Errorf("claim=%q evidence=%q", in.Claim, in.Evidence)
	}
}
