package services

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
)

// Memory evidence grades. They say how much a reader can trust a memory,
// separately from who wrote it (created_by):
//
//   - confirmed: an operator checked it. Only an operator action sets it.
//   - observed: a fact seen directly (config, tool behaviour, a timeline),
//     not a cause.
//   - unverified: a cause nobody confirmed, or a legacy row nobody assessed.
//
// The gateway's memory.write applies the same rules
// (mcp-gateway/internal/tools/knowledge/evidence.go); both modules run
// tests/fixtures/memory_evidence_cases.json.
const (
	MemoryEvidenceConfirmed  = "confirmed"
	MemoryEvidenceObserved   = "observed"
	MemoryEvidenceUnverified = "unverified"
)

// Memory claim kinds. A causal claim says that X caused Y; a factual claim
// says what is. Empty = not assessed (legacy rows).
const (
	MemoryClaimCausal  = "causal"
	MemoryClaimFactual = "factual"
)

// MemoryEvidenceNoteMaxLen caps evidence_note in bytes.
const MemoryEvidenceNoteMaxLen = 500

// MemoryLegacyEvidenceNote marks rows graded by the upgrade backfill.
const MemoryLegacyEvidenceNote = "legacy: not assessed"

// ValidMemoryEvidence reports whether s is a grade.
func ValidMemoryEvidence(s string) bool {
	return s == MemoryEvidenceConfirmed || s == MemoryEvidenceObserved || s == MemoryEvidenceUnverified
}

// validMemoryClaim reports whether s is a claim kind or empty.
func validMemoryClaim(s string) bool {
	return s == "" || s == MemoryClaimCausal || s == MemoryClaimFactual
}

// causalPattern is a net for causal wording in agent-written text. A model
// can label a cause "factual"; this catches the common phrasings. It only
// ever lowers a grade, so a false positive costs an "unverified" badge.
var causalPattern = regexp.MustCompile(`(?i)\b(caus(e|es|ed|ing)|root[- ]causes?|because|due to|led to|leads to|resulted in|results in|triggered by)\b`)

// memoryLooksCausal reports whether the description or body uses causal
// wording.
func memoryLooksCausal(description, body string) bool {
	return causalPattern.MatchString(description) || causalPattern.MatchString(body)
}

// defaultMemoryEvidence fills an empty grade: a causal claim is unverified,
// anything else observed.
func defaultMemoryEvidence(m *database.Memory) {
	if m.Evidence == "" {
		if m.Claim == MemoryClaimCausal {
			m.Evidence = MemoryEvidenceUnverified
		} else {
			m.Evidence = MemoryEvidenceObserved
		}
	}
}

// validateMemoryEvidence trims the evidence fields, checks the enums and the
// note cap, and fills an empty grade. It is the operator rule set: any grade
// is allowed.
func validateMemoryEvidence(m *database.Memory) error {
	m.Evidence = strings.TrimSpace(m.Evidence)
	m.Claim = strings.TrimSpace(m.Claim)
	m.EvidenceNote = strings.TrimSpace(m.EvidenceNote)
	if m.Evidence != "" && !ValidMemoryEvidence(m.Evidence) {
		return fmt.Errorf("invalid evidence %q (allowed: confirmed, observed, unverified)", m.Evidence)
	}
	if !validMemoryClaim(m.Claim) {
		return fmt.Errorf("invalid claim %q (allowed: causal, factual)", m.Claim)
	}
	if len(m.EvidenceNote) > MemoryEvidenceNoteMaxLen {
		return fmt.Errorf("evidence_note exceeds %d bytes", MemoryEvidenceNoteMaxLen)
	}
	defaultMemoryEvidence(m)
	return nil
}

// ApplyAgentMemoryEvidence applies the rules for memories that an agent or
// a model wrote (gateway memory.write, proposal content, disk ingest, the
// post-incident extractor): claim defaults to factual, causal wording makes
// the claim causal, a causal claim is always unverified, and confirmed is
// refused. A downgrade is explained in the note. The note is cut UTF-8 safe
// to MemoryEvidenceNoteMaxLen (truncated = true). It returns an error only
// for values that are not grades or claims.
func ApplyAgentMemoryEvidence(m *database.Memory) (truncated bool, err error) {
	m.Evidence = strings.TrimSpace(m.Evidence)
	m.Claim = strings.TrimSpace(m.Claim)
	m.EvidenceNote = strings.TrimSpace(m.EvidenceNote)
	if m.Evidence != "" && !ValidMemoryEvidence(m.Evidence) {
		return false, fmt.Errorf("invalid evidence %q (allowed: observed, unverified)", m.Evidence)
	}
	if !validMemoryClaim(m.Claim) {
		return false, fmt.Errorf("invalid claim %q (allowed: causal, factual)", m.Claim)
	}
	if m.Claim == "" {
		m.Claim = MemoryClaimFactual
	}
	// A feedback memory is a rule, not a cause: its **Why:** line says
	// "because" by format, so the net does not run on it.
	if m.Claim != MemoryClaimCausal && m.Type != MemoryTypeFeedback && memoryLooksCausal(m.Description, m.Body) {
		m.Claim = MemoryClaimCausal
	}
	switch {
	case m.Evidence == MemoryEvidenceConfirmed:
		m.Evidence = MemoryEvidenceUnverified
		m.EvidenceNote = appendEvidenceNote(m.EvidenceNote, "only an operator can confirm; stored as unverified")
	case m.Claim == MemoryClaimCausal && m.Evidence == MemoryEvidenceObserved:
		m.Evidence = MemoryEvidenceUnverified
		m.EvidenceNote = appendEvidenceNote(m.EvidenceNote, "causal claim; stored as unverified")
	}
	defaultMemoryEvidence(m)
	m.EvidenceNote, truncated = cutEvidenceNote(m.EvidenceNote)
	return truncated, nil
}

// Notes that MergeStoredMemoryEvidence adds. The gateway uses the same text.
const (
	memoryConfirmationResetNote = "operator confirmation reset: content changed"
	memoryRaiseNeedsNote        = "raising unverified to observed needs an evidence_note that says what was observed; stays unverified"
)

// MergeStoredMemoryEvidence applies the stored row to an agent or model
// update (after ApplyAgentMemoryEvidence). A rewrite is not a check:
//
//   - evidence omitted and the text unchanged: the stored grade, claim and
//     note stay (a type or note change does not touch the grade);
//   - evidence omitted and the text changed: a stored unverified stays
//     unverified; a stored confirmed falls to the agent default, with a note;
//   - observed given on a stored unverified row without an evidence_note: the
//     row stays unverified and the note says why (raiseRefused = true);
//   - note omitted: the stored note stays, except the legacy backfill note
//     on changed text.
//
// The gateway's keepStoredEvidence mirrors these rules.
func MergeStoredMemoryEvidence(m, stored *database.Memory, evidenceOmitted bool) (raiseRefused bool) {
	textChanged := strings.TrimSpace(stored.Description) != strings.TrimSpace(m.Description) ||
		strings.TrimSpace(stored.Body) != strings.TrimSpace(m.Body)
	noteOmitted := m.EvidenceNote == ""
	switch {
	case evidenceOmitted && !textChanged && stored.Evidence != "":
		m.Evidence = stored.Evidence
		if stored.Claim != "" {
			m.Claim = stored.Claim
		}
	case evidenceOmitted && stored.Evidence == MemoryEvidenceUnverified:
		m.Evidence = MemoryEvidenceUnverified
	case evidenceOmitted && stored.Evidence == MemoryEvidenceConfirmed:
		m.EvidenceNote = appendEvidenceNote(m.EvidenceNote, memoryConfirmationResetNote)
	case !evidenceOmitted && noteOmitted && stored.Evidence == MemoryEvidenceUnverified && m.Evidence == MemoryEvidenceObserved:
		m.Evidence = MemoryEvidenceUnverified
		m.EvidenceNote = memoryRaiseNeedsNote
		raiseRefused = true
	}
	if noteOmitted && m.EvidenceNote == "" && (!textChanged || stored.EvidenceNote != MemoryLegacyEvidenceNote) {
		m.EvidenceNote = stored.EvidenceNote
	}
	m.EvidenceNote, _ = cutEvidenceNote(m.EvidenceNote)
	return raiseRefused
}

func appendEvidenceNote(note, reason string) string {
	if note == "" {
		return reason
	}
	return note + " (" + reason + ")"
}

// cutEvidenceNote cuts s to MemoryEvidenceNoteMaxLen bytes without
// splitting a UTF-8 rune.
func cutEvidenceNote(s string) (string, bool) {
	if len(s) <= MemoryEvidenceNoteMaxLen {
		return s, false
	}
	cut := MemoryEvidenceNoteMaxLen
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return strings.TrimSpace(s[:cut]), true
}
