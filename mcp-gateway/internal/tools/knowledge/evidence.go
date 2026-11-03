package knowledge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/akmatori/mcp-gateway/internal/database"
)

// Evidence grades and claim kinds, as in the main API's
// internal/services/memory_evidence.go. Both modules run
// tests/fixtures/memory_evidence_cases.json, so a drift fails both.
const (
	evidenceConfirmed  = "confirmed"
	evidenceObserved   = "observed"
	evidenceUnverified = "unverified"
	claimCausal        = "causal"
	claimFactual       = "factual"
	evidenceNoteMaxLen = 500 // bytes
)

// causalPattern is the API's net for causal wording (same expression).
var causalPattern = regexp.MustCompile(`(?i)\b(caus(e|es|ed|ing)|root[- ]causes?|because|due to|led to|leads to|resulted in|results in|triggered by)\b`)

func validEvidence(s string) bool {
	return s == evidenceConfirmed || s == evidenceObserved || s == evidenceUnverified
}

func validClaim(s string) bool {
	return s == "" || s == claimCausal || s == claimFactual
}

// applyAgentEvidence is the API's ApplyAgentMemoryEvidence on a
// memory.write input: claim defaults to factual, causal wording makes the
// claim causal, a causal claim is unverified, and confirmed is refused. A
// downgrade is explained in the note; the note is cut UTF-8 safe to 500
// bytes (truncated = true).
func applyAgentEvidence(in *memoryInput) (truncated bool, err error) {
	in.Evidence = strings.TrimSpace(in.Evidence)
	in.Claim = strings.TrimSpace(in.Claim)
	in.EvidenceNote = strings.TrimSpace(in.EvidenceNote)
	if in.Evidence != "" && !validEvidence(in.Evidence) {
		return false, fmt.Errorf("invalid evidence %q (allowed: observed, unverified)", in.Evidence)
	}
	if !validClaim(in.Claim) {
		return false, fmt.Errorf("invalid claim %q (allowed: causal, factual)", in.Claim)
	}
	if in.Claim == "" {
		in.Claim = claimFactual
	}
	// A feedback memory is a rule, not a cause: its **Why:** line says
	// "because" by format, so the net does not run on it.
	if in.Claim != claimCausal && in.Type != typeFeedback && (causalPattern.MatchString(in.Description) || causalPattern.MatchString(in.Body)) {
		in.Claim = claimCausal
	}
	switch {
	case in.Evidence == evidenceConfirmed:
		in.Evidence = evidenceUnverified
		in.EvidenceNote = appendEvidenceNote(in.EvidenceNote, "only an operator can confirm; stored as unverified")
	case in.Claim == claimCausal && in.Evidence == evidenceObserved:
		in.Evidence = evidenceUnverified
		in.EvidenceNote = appendEvidenceNote(in.EvidenceNote, "causal claim; stored as unverified")
	}
	if in.Evidence == "" {
		if in.Claim == claimCausal {
			in.Evidence = evidenceUnverified
		} else {
			in.Evidence = evidenceObserved
		}
	}
	in.EvidenceNote, truncated = cutUTF8(in.EvidenceNote, evidenceNoteMaxLen)
	return truncated, nil
}

// Notes that mergeStoredEvidence adds (the API's MergeStoredMemoryEvidence
// uses the same text).
const (
	confirmationResetNote = "operator confirmation reset: content changed"
	raiseNeedsNote        = "raising unverified to observed needs an evidence_note that says what was observed; stays unverified"
	legacyEvidenceNote    = "legacy: not assessed"
)

// mergeStoredEvidence is the API's MergeStoredMemoryEvidence: the stored row
// rules an update's grade, because a rewrite is not a check.
//
//   - evidence omitted and the text unchanged: the stored grade, claim and
//     note stay;
//   - evidence omitted and the text changed: a stored unverified stays
//     unverified; a stored confirmed falls to the agent default, with a note;
//   - observed given on a stored unverified row without an evidence_note: the
//     row stays unverified and the note says why (raiseRefused = true);
//   - note omitted: the stored note stays, except the legacy backfill note
//     on changed text.
func mergeStoredEvidence(in *memoryInput, stored *database.Memory, evidenceOmitted bool) (raiseRefused bool) {
	textChanged := strings.TrimSpace(stored.Description) != strings.TrimSpace(in.Description) ||
		strings.TrimSpace(stored.Body) != strings.TrimSpace(in.Body)
	noteOmitted := in.EvidenceNote == ""
	switch {
	case evidenceOmitted && !textChanged && stored.Evidence != "":
		in.Evidence = stored.Evidence
		if stored.Claim != "" {
			in.Claim = stored.Claim
		}
	case evidenceOmitted && stored.Evidence == evidenceUnverified:
		in.Evidence = evidenceUnverified
	case evidenceOmitted && stored.Evidence == evidenceConfirmed:
		in.EvidenceNote = appendEvidenceNote(in.EvidenceNote, confirmationResetNote)
	case !evidenceOmitted && noteOmitted && stored.Evidence == evidenceUnverified && in.Evidence == evidenceObserved:
		in.Evidence = evidenceUnverified
		in.EvidenceNote = raiseNeedsNote
		raiseRefused = true
	}
	if noteOmitted && in.EvidenceNote == "" && (!textChanged || stored.EvidenceNote != legacyEvidenceNote) {
		in.EvidenceNote = stored.EvidenceNote
	}
	in.EvidenceNote, _ = cutUTF8(in.EvidenceNote, evidenceNoteMaxLen)
	return raiseRefused
}

func appendEvidenceNote(note, reason string) string {
	if note == "" {
		return reason
	}
	return note + " (" + reason + ")"
}
