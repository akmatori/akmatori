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
// It also pins the multi-sub-query shape so the user-turn reminder stays in
// sync with DefaultIncidentManagerPrompt's runbook-search section: a single
// qmd.query carrying TWO searches[] entries (verbatim 2x-weighted first,
// keywords second) with retry guidance capped at 3 total calls.
func TestPrependGuidance_ScopesRunbookSearchToRunbooksCollection(t *testing.T) {
	out := PrependGuidance("test task")
	for _, want := range []string{
		`gateway_call("qmd.query"`,
		`"collection": "runbooks"`,
		`"type": "lex"`,
		`gateway_call("qmd.get"`,
		// Multi-sub-query markers — these must stay in sync with
		// DefaultIncidentManagerPrompt's runbook-search section.
		"sub-query 1",
		"sub-query 2",
		"automatically",
		"weighted 2x",
		"Cap total qmd.query calls at 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("PrependGuidance() missing %q\nfull output:\n%s", want, out)
		}
	}

	// The verbatim placeholder must come BEFORE the keywords placeholder in
	// the searches[] array — RRF gives the first entry the 2x weight.
	verbatimIdx := strings.Index(out, "<verbatim alert excerpt")
	keywordsIdx := strings.Index(out, "<short keywords>")
	if verbatimIdx < 0 || keywordsIdx < 0 {
		t.Fatalf("PrependGuidance() missing verbatim or keywords placeholder, got:\n%s", out)
	}
	if verbatimIdx > keywordsIdx {
		t.Errorf("PrependGuidance() must list verbatim sub-query before keywords sub-query (RRF 2x weight is on the first entry)")
	}

	if !strings.Contains(out, "test task") {
		t.Errorf("PrependGuidance() should append the user task, got:\n%s", out)
	}
}
