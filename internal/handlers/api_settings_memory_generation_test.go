package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// regenAllSkillService records RegenerateAllSkillMds calls, which the
// settings handler makes from a goroutine.
type regenAllSkillService struct {
	services.SkillIncidentManager
	calls chan struct{}
}

func (r *regenAllSkillService) RegenerateAllSkillMds() error {
	r.calls <- struct{}{}
	return nil
}

func putGeneralSettings(t *testing.T, h *APIHandler, body string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/general", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.handleGeneralSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT %s: status %d: %s", body, rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestHandleGeneralSettings_MemoryGeneration(t *testing.T) {
	testhelpers.NewGlobalSQLiteDB(t, &database.GeneralSettings{})
	skills := &regenAllSkillService{calls: make(chan struct{}, 4)}
	h := NewAPIHandler(skills, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	// GET shows the effective default (on) for an install that never set it.
	rec := httptest.NewRecorder()
	h.handleGeneralSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings/general", nil))
	var got map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, ok := got["memory_generation_enabled"].(bool); !ok || !v {
		t.Errorf("default memory_generation_enabled = %v, want true", got["memory_generation_enabled"])
	}

	// Turning it off persists and regenerates every SKILL.md.
	if out := putGeneralSettings(t, h, `{"memory_generation_enabled": false}`); out["memory_generation_enabled"] != false {
		t.Errorf("PUT response memory_generation_enabled = %v", out["memory_generation_enabled"])
	}
	gs, err := database.GetOrCreateGeneralSettings()
	if err != nil || gs.GetMemoryGenerationEnabled() {
		t.Fatalf("stored gate = %+v, err %v; want off", gs, err)
	}
	select {
	case <-skills.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("SKILL.md regeneration not triggered on change")
	}

	// The same value again, or an unrelated field, does not regenerate.
	putGeneralSettings(t, h, `{"memory_generation_enabled": false}`)
	putGeneralSettings(t, h, `{"incident_merge_enabled": true}`)
	select {
	case <-skills.calls:
		t.Error("SKILL.md regenerated without a gate change")
	case <-time.After(100 * time.Millisecond):
	}
}
