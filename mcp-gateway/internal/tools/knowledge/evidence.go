package knowledge

import (
	"fmt"
	"regexp"
	"strings"
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
	if in.Claim != claimCausal && (causalPattern.MatchString(in.Description) || causalPattern.MatchString(in.Body)) {
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
	if len(in.EvidenceNote) > evidenceNoteMaxLen {
		cut := evidenceNoteMaxLen
		for cut > 0 && (in.EvidenceNote[cut]&0xC0) == 0x80 {
			cut--
		}
		in.EvidenceNote = strings.TrimSpace(in.EvidenceNote[:cut])
		truncated = true
	}
	return truncated, nil
}

func appendEvidenceNote(note, reason string) string {
	if note == "" {
		return reason
	}
	return note + " (" + reason + ")"
}
