package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

const (
	testPrivateKey  = "-----BEGIN OPENSSH PRIVATE KEY-----\nSECRETKEYMATERIAL\n-----END OPENSSH PRIVATE KEY-----"
	testZabbixToken = "zbx-real-secret-token"
)

// promptOnlySkillService satisfies SkillIncidentManager for the skills
// handlers, which only call GetSkillPrompt on the read paths under test.
type promptOnlySkillService struct {
	services.SkillIncidentManager
}

func (promptOnlySkillService) GetSkillPrompt(string) (string, error) { return "", nil }

// newToolsTestHandler wires a real ToolService on an in-memory SQLite DB and
// seeds an SSH instance (with a private key) and a Zabbix instance (with a
// token).
func newToolsTestHandler(t *testing.T) (*APIHandler, *database.ToolInstance, *database.ToolInstance) {
	t.Helper()
	db := testhelpers.NewGlobalSQLiteDB(t, &database.ToolType{}, &database.ToolInstance{}, &database.Skill{})

	sshType := database.ToolType{Name: "ssh"}
	zbxType := database.ToolType{Name: "zabbix"}
	for _, tt := range []*database.ToolType{&sshType, &zbxType} {
		if err := db.Create(tt).Error; err != nil {
			t.Fatalf("create tool type: %v", err)
		}
	}

	sshTool := testhelpers.NewToolInstanceBuilder().
		WithToolTypeID(sshType.ID).WithName("Prod SSH").WithLogicalName("prod-ssh").
		WithSettings(database.JSONB{
			"ssh_keys": []interface{}{map[string]interface{}{
				"id": "k1", "name": "default", "private_key": testPrivateKey, "is_default": true,
			}},
			"ssh_hosts": []interface{}{map[string]interface{}{"hostname": "web1", "address": "10.0.0.1"}},
		}).Build()
	zbxTool := testhelpers.NewToolInstanceBuilder().
		WithToolTypeID(zbxType.ID).WithName("Prod Zabbix").WithLogicalName("prod-zabbix").
		WithSettings(database.JSONB{
			"zabbix_url":   "https://zbx.example.com",
			"zabbix_token": testZabbixToken,
		}).Build()
	for _, ti := range []*database.ToolInstance{&sshTool, &zbxTool} {
		if err := db.Create(ti).Error; err != nil {
			t.Fatalf("create tool instance: %v", err)
		}
	}

	h := NewAPIHandler(promptOnlySkillService{}, services.NewToolService(), nil, nil, nil, nil, nil, nil, nil, nil, nil)
	return h, &sshTool, &zbxTool
}

func assertNoToolSecrets(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "SECRETKEYMATERIAL") || strings.Contains(body, "private_key") {
		t.Errorf("response leaks SSH private key: %s", body)
	}
	if strings.Contains(body, testZabbixToken) {
		t.Errorf("response leaks zabbix_token: %s", body)
	}
}

func TestToolResponses_DoNotLeakSecrets(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		call   func(h *APIHandler, w http.ResponseWriter, r *http.Request)
		status int
	}{
		{"list tools", http.MethodGet, "/api/tools", "", (*APIHandler).handleTools, http.StatusOK},
		{"get ssh tool", http.MethodGet, "/api/tools/%[1]d", "", (*APIHandler).handleToolByID, http.StatusOK},
		{"get zabbix tool", http.MethodGet, "/api/tools/%[2]d", "", (*APIHandler).handleToolByID, http.StatusOK},
		{"put zabbix tool", http.MethodPut, "/api/tools/%[2]d", `{"enabled":true}`, (*APIHandler).handleToolByID, http.StatusOK},
		{"put ssh tool", http.MethodPut, "/api/tools/%[1]d", `{"name":"Prod SSH 2"}`, (*APIHandler).handleToolByID, http.StatusOK},
		{"create tool", http.MethodPost, "/api/tools", `{"tool_type_id":2,"name":"New Zabbix","settings":{"zabbix_token":"` + testZabbixToken + `"}}`, (*APIHandler).handleTools, http.StatusCreated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, sshTool, zbxTool := newToolsTestHandler(t)
			path := fmt.Sprintf(tt.path, sshTool.ID, zbxTool.ID)
			if !strings.Contains(tt.path, "%") {
				path = tt.path
			}
			req := httptest.NewRequest(tt.method, path, bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()
			tt.call(h, w, req)

			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.status, w.Body.String())
			}
			assertNoToolSecrets(t, w.Body.String())
		})
	}
}

func TestToolList_MasksSecretsButKeepsShape(t *testing.T) {
	h, _, _ := newToolsTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/tools", nil)
	w := httptest.NewRecorder()
	h.handleTools(w, req)

	var tools []database.ToolInstance
	if err := json.Unmarshal(w.Body.Bytes(), &tools); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	for _, ti := range tools {
		switch ti.Name {
		case "Prod SSH":
			keys, _ := ti.Settings["ssh_keys"].([]interface{})
			if len(keys) != 1 || keys[0].(map[string]interface{})["name"] != "default" {
				t.Errorf("ssh key metadata missing: %v", ti.Settings["ssh_keys"])
			}
			if _, ok := ti.Settings["ssh_hosts"]; !ok {
				t.Error("ssh_hosts must still be returned")
			}
		case "Prod Zabbix":
			if ti.Settings["zabbix_token"] != database.ToolSecretMask {
				t.Errorf("zabbix_token = %v, want mask", ti.Settings["zabbix_token"])
			}
			if ti.Settings["zabbix_url"] != "https://zbx.example.com" {
				t.Errorf("zabbix_url = %v", ti.Settings["zabbix_url"])
			}
		}
	}
}

func TestToolUpdate_PartialAndMaskedRoundTrip(t *testing.T) {
	h, sshTool, zbxTool := newToolsTestHandler(t)

	put := func(id uint, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/tools/%d", id), bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		h.handleToolByID(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT status = %d; body=%s", w.Code, w.Body.String())
		}
		return w
	}
	stored := func(id uint) *database.ToolInstance {
		ti, err := services.NewToolService().GetToolInstance(id)
		if err != nil {
			t.Fatalf("GetToolInstance: %v", err)
		}
		return ti
	}

	// Omitted fields leave the row alone: a name-only PUT must not disable the
	// tool, wipe its settings or re-derive its logical name.
	put(zbxTool.ID, `{"name":"Renamed Zabbix"}`)
	got := stored(zbxTool.ID)
	if got.Name != "Renamed Zabbix" || got.LogicalName != "prod-zabbix" || !got.Enabled {
		t.Errorf("name-only PUT: name=%q logical=%q enabled=%v", got.Name, got.LogicalName, got.Enabled)
	}
	if got.Settings["zabbix_token"] != testZabbixToken {
		t.Errorf("name-only PUT changed settings: %v", got.Settings)
	}

	// The web UI sends back the full object it read, with the secret masked.
	put(zbxTool.ID, fmt.Sprintf(`{"name":"Renamed Zabbix","logical_name":"","enabled":false,"settings":{"zabbix_url":"https://new.example.com","zabbix_token":%q}}`, database.ToolSecretMask))
	got = stored(zbxTool.ID)
	if got.Settings["zabbix_token"] != testZabbixToken {
		t.Errorf("masked round trip overwrote token: %v", got.Settings["zabbix_token"])
	}
	if got.Settings["zabbix_url"] != "https://new.example.com" || got.Enabled || got.LogicalName != "renamed-zabbix" {
		t.Errorf("full PUT not applied: %+v", got)
	}

	// SSH keys survive a settings PUT that omits them (the UI strips ssh_keys).
	put(sshTool.ID, `{"settings":{"ssh_hosts":[]}}`)
	keys, _ := stored(sshTool.ID).Settings["ssh_keys"].([]interface{})
	if len(keys) != 1 || keys[0].(map[string]interface{})["private_key"] != testPrivateKey {
		t.Errorf("ssh_keys not preserved: %v", keys)
	}
}
