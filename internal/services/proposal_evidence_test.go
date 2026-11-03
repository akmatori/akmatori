package services

import (
	"context"
	"errors"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// Approval applies the agent rules: the operator approves the change, not
// every claim in it, so a drafted cause stays unverified.
func TestApprove_MemoryNew_CausalStaysUnverified(t *testing.T) {
	db := newProposalTestDB(t)
	memories := &fakeMemories{}
	svc := NewProposalService(db, &fakeRunbooks{}, memories, &fakeCrons{}, &fakeSkills{})
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindMemoryNew, Title: "Record ingest gap cause",
		ProposedContent: `{"scope":"global","type":"fact","name":"ingest-gap","description":"high load caused the ingest gap","body":"b","evidence":"confirmed"}`,
	})
	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	up := memories.upserted[0]
	if up.Evidence != MemoryEvidenceUnverified || up.Claim != MemoryClaimCausal || up.CreatedBy != database.ProposalCreatedByOperator {
		t.Errorf("upsert: %+v", up)
	}
}

func TestApprove_MemoryNew_FactDefaultsObserved(t *testing.T) {
	db := newProposalTestDB(t)
	memories := &fakeMemories{}
	svc := NewProposalService(db, &fakeRunbooks{}, memories, &fakeCrons{}, &fakeSkills{})
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindMemoryNew, Title: "Record redis port",
		ProposedContent: `{"scope":"global","type":"fact","name":"redis-port","description":"redis prod listens on 16379","body":"b"}`,
	})
	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if up := memories.upserted[0]; up.Evidence != MemoryEvidenceObserved || up.Claim != MemoryClaimFactual {
		t.Errorf("upsert: %+v", up)
	}
}

// A snapshot from before evidence grades has no evidence fields: they are
// not compared, so the upgrade alone does not supersede a pending proposal.
func TestApprove_MemoryUpdate_LegacySnapshotIgnoresEvidence(t *testing.T) {
	db := newProposalTestDB(t)
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, &fakeCrons{}, &fakeSkills{})
	db.Create(&database.Memory{Scope: "global", Type: "fact", Name: "web-1", Description: "d", Body: "b", Evidence: "unverified", EvidenceNote: "legacy: not assessed"})
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindMemoryUpdate, Title: "Refresh", TargetRef: "global/web-1",
		CurrentSnapshot: `{"scope":"global","type":"fact","name":"web-1","description":"d","body":"b"}`,
		ProposedContent: `{"scope":"global","type":"fact","name":"web-1","description":"d2","body":"b2"}`,
	})
	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatalf("legacy snapshot must stay approvable: %v", err)
	}
}

// A new snapshot carries the grade; a grade change since then is staleness.
func TestApprove_MemoryUpdate_EvidenceChangeIsStale(t *testing.T) {
	db := newProposalTestDB(t)
	svc := NewProposalService(db, &fakeRunbooks{}, &fakeMemories{}, &fakeCrons{}, &fakeSkills{})
	db.Create(&database.Memory{Scope: "global", Type: "fact", Name: "web-1", Description: "d", Body: "b", Evidence: "confirmed", CreatedBy: "operator"})
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindMemoryUpdate, Title: "Refresh", TargetRef: "global/web-1",
		CurrentSnapshot: `{"scope":"global","type":"fact","name":"web-1","description":"d","body":"b","evidence":"unverified","claim":"","evidence_note":""}`,
		ProposedContent: `{"scope":"global","type":"fact","name":"web-1","description":"d2","body":"b2"}`,
	})
	_, err := svc.Approve(context.Background(), "p1")
	if !errors.Is(err, ErrProposalStale) {
		t.Fatalf("err = %v, want ErrProposalStale", err)
	}
}

// Approving an update without a grade keeps an unverified memory
// unverified and keeps its note.
func TestApprove_MemoryUpdate_OmittedEvidenceKeepsStoredGrade(t *testing.T) {
	db := newProposalTestDB(t)
	memories := &fakeMemories{}
	svc := NewProposalService(db, &fakeRunbooks{}, memories, &fakeCrons{}, &fakeSkills{})
	db.Create(&database.Memory{Scope: "global", Type: "fact", Name: "web-1", Description: "d", Body: "b", Evidence: "unverified", EvidenceNote: "legacy: not assessed"})
	seedProposal(t, db, &database.Proposal{
		UUID: "p1", Kind: database.ProposalKindMemoryUpdate, Title: "Refresh", TargetRef: "global/web-1",
		CurrentSnapshot: `{"scope":"global","type":"fact","name":"web-1","description":"d","body":"b"}`,
		ProposedContent: `{"scope":"global","type":"fact","name":"web-1","description":"d2","body":"b2"}`,
	})
	if _, err := svc.Approve(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	if up := memories.upserted[0]; up.Evidence != MemoryEvidenceUnverified || up.EvidenceNote != "legacy: not assessed" {
		t.Errorf("upsert: %+v", up)
	}
}
