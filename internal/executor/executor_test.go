package executor

import (
	"strings"
	"testing"
)

// TestPrependGuidance_ScopesRunbookSearchToRunbooksCollection guards against
// the runbook-search guidance regressing to an unscoped qmd.query. With the
// memories collection now indexed by QMD, an unscoped search would surface
// memory documents during the "search runbooks first" workflow and the
// agent might fetch/follow them as runbooks.
//
// It also pins the {lex, vec, hyde} triplet shape so the user-turn reminder
// stays in sync with DefaultIncidentManagerPrompt's runbook-search section:
// a single qmd.query carrying THREE searches[] entries — one per retrieval
// mode (lex/vec/hyde), all three carrying the same natural-language alert
// summary, fused by QMD via RRF, with retry guidance capped at 3 total calls.
func TestPrependGuidance_ScopesRunbookSearchToRunbooksCollection(t *testing.T) {
	out := PrependGuidance("test task")
	for _, want := range []string{
		`gateway_call("qmd.query"`,
		`"collections": ["runbooks"]`,
		`"type": "lex"`,
		`"type": "vec"`,
		`"type": "hyde"`,
		`gateway_call("qmd.get"`,
		"Cap total qmd.query calls at 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrependGuidance() missing %q\nfull output:\n%s", want, out)
		}
	}

	if !strings.Contains(out, "test task") {
		t.Errorf("PrependGuidance() should append the user task, got:\n%s", out)
	}
}

// TestPrependGuidance_SingleQMDQueryWithOrderedTriplet pins the structural
// invariant that the user-turn reminder issues exactly ONE
// gateway_call("qmd.query", ...) with the three sub-queries in lex→vec→hyde
// order. The substring assertions above would still pass if a future edit
// split the call into three separate qmd.query invocations or reordered the
// modes — this test catches that drift.
func TestPrependGuidance_SingleQMDQueryWithOrderedTriplet(t *testing.T) {
	out := PrependGuidance("test task")

	if got := strings.Count(out, `gateway_call("qmd.query"`); got != 1 {
		t.Errorf("expected exactly 1 gateway_call(\"qmd.query\"...) in guidance, got %d", got)
	}

	lexIdx := strings.Index(out, `"type": "lex"`)
	vecIdx := strings.Index(out, `"type": "vec"`)
	hydeIdx := strings.Index(out, `"type": "hyde"`)
	if lexIdx < 0 || vecIdx < 0 || hydeIdx < 0 {
		t.Fatalf("missing one of the three sub-query type markers: lex=%d vec=%d hyde=%d", lexIdx, vecIdx, hydeIdx)
	}
	if !(lexIdx < vecIdx && vecIdx < hydeIdx) {
		t.Errorf("triplet must appear in lex→vec→hyde order, got lex=%d vec=%d hyde=%d", lexIdx, vecIdx, hydeIdx)
	}
}
