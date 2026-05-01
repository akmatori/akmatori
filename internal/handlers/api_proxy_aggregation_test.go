package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/gorm"
)

func setupProxyAndAggregationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testhelpers.SetupSQLiteTestDB(t, &database.ProxySettings{}, &database.AggregationSettings{})
}

func TestAPIHandler_GetProxySettings_ReturnsDefaultsAndMasksPassword(t *testing.T) {
	db := setupProxyAndAggregationTestDB(t)

	settings, err := database.GetOrCreateProxySettings()
	if err != nil {
		t.Fatalf("create proxy settings: %v", err)
	}
	settings.ProxyURL = "http://user:secret123@proxy.example.com:8080"
	settings.NoProxy = "localhost,127.0.0.1"
	if err := database.UpdateProxySettings(settings); err != nil {
		t.Fatalf("update proxy settings fixture: %v", err)
	}

	var persisted database.ProxySettings
	if err := db.First(&persisted, settings.ID).Error; err != nil {
		t.Fatalf("reload proxy settings fixture: %v", err)
	}

	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/proxy", nil)
	w := httptest.NewRecorder()

	h.GetProxySettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got["proxy_url"] != "http://user:%2A%2A%2A%2A@proxy.example.com:8080" {
		t.Errorf("proxy_url = %v, want masked URL", got["proxy_url"])
	}
	if got["no_proxy"] != persisted.NoProxy {
		t.Errorf("no_proxy = %v, want %q", got["no_proxy"], persisted.NoProxy)
	}

	services, ok := got["services"].(map[string]any)
	if !ok {
		t.Fatalf("services type = %T, want map[string]any", got["services"])
	}
	if openai := services["openai"].(map[string]any); openai["enabled"] != true || openai["supported"] != true {
		t.Errorf("openai service = %#v, want enabled and supported", openai)
	}
	if ssh := services["ssh"].(map[string]any); ssh["enabled"] != false || ssh["supported"] != false {
		t.Errorf("ssh service = %#v, want disabled and unsupported", ssh)
	}
}

func TestAPIHandler_HandleProxySettings_TableDriven(t *testing.T) {
	setupProxyAndAggregationTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name           string
		method         string
		body           string
		wantStatus     int
		wantBodySubstr string
		verify         func(t *testing.T)
	}{
		{
			name:           "rejects unsupported method",
			method:         http.MethodPost,
			wantStatus:     http.StatusMethodNotAllowed,
			wantBodySubstr: "Method not allowed",
		},
		{
			name:           "rejects invalid json",
			method:         http.MethodPut,
			body:           "{",
			wantStatus:     http.StatusBadRequest,
			wantBodySubstr: "Invalid JSON",
		},
		{
			name:           "rejects invalid proxy url",
			method:         http.MethodPut,
			body:           `{"proxy_url":"ftp://proxy.example.com"}`,
			wantStatus:     http.StatusBadRequest,
			wantBodySubstr: "Invalid proxy URL format",
		},
		{
			name:       "updates proxy settings",
			method:     http.MethodPut,
			body:       `{"proxy_url":"https://user:pass@proxy.example.com:8443","no_proxy":"localhost,.svc","services":{"openai":{"enabled":true},"slack":{"enabled":false},"zabbix":{"enabled":true}}}`,
			wantStatus: http.StatusOK,
			verify: func(t *testing.T) {
				t.Helper()
				settings, err := database.GetOrCreateProxySettings()
				if err != nil {
					t.Fatalf("reload proxy settings: %v", err)
				}
				if settings.ProxyURL != "https://user:pass@proxy.example.com:8443" {
					t.Errorf("ProxyURL = %q, want updated value", settings.ProxyURL)
				}
				if settings.NoProxy != "localhost,.svc" {
					t.Errorf("NoProxy = %q, want updated value", settings.NoProxy)
				}
				if !settings.OpenAIEnabled || settings.SlackEnabled || !settings.ZabbixEnabled {
					t.Errorf("service toggles = %+v, want openai=true slack=false zabbix=true", settings)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body *bytes.Reader
			if tt.body == "" {
				body = bytes.NewReader(nil)
			} else {
				body = bytes.NewReader([]byte(tt.body))
			}

			req := httptest.NewRequest(tt.method, "/api/settings/proxy", body)
			w := httptest.NewRecorder()
			h.handleProxySettings(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantBodySubstr != "" && !bytes.Contains(w.Body.Bytes(), []byte(tt.wantBodySubstr)) {
				t.Fatalf("body = %q, want substring %q", w.Body.String(), tt.wantBodySubstr)
			}
			if tt.verify != nil {
				tt.verify(t)
			}
		})
	}
}

func TestAPIHandler_AggregationSettings_RoundTrip(t *testing.T) {
	setupProxyAndAggregationTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/aggregation", nil)
	getW := httptest.NewRecorder()
	h.handleGetAggregationSettings(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("initial GET status = %d, want %d; body=%s", getW.Code, http.StatusOK, getW.Body.String())
	}

	var initial database.AggregationSettings
	if err := json.NewDecoder(getW.Body).Decode(&initial); err != nil {
		t.Fatalf("decode initial aggregation settings: %v", err)
	}
	if !initial.Enabled || initial.MaxIncidentsToAnalyze != 20 {
		t.Fatalf("unexpected defaults: %+v", initial)
	}

	updated := initial
	updated.Enabled = false
	updated.MergeConfidenceThreshold = 0.91
	updated.MaxIncidentsToAnalyze = 42
	updated.RecorrelationIntervalMinutes = 9

	body, err := json.Marshal(updated)
	if err != nil {
		t.Fatalf("marshal updated settings: %v", err)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/settings/aggregation", bytes.NewReader(body))
	putW := httptest.NewRecorder()
	h.handleUpdateAggregationSettings(putW, putReq)

	if putW.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d; body=%s", putW.Code, http.StatusOK, putW.Body.String())
	}

	var saved database.AggregationSettings
	if err := json.NewDecoder(putW.Body).Decode(&saved); err != nil {
		t.Fatalf("decode updated aggregation settings: %v", err)
	}
	if saved.ID != initial.ID {
		t.Errorf("ID = %d, want existing ID %d", saved.ID, initial.ID)
	}
	if saved.Enabled != false || saved.MergeConfidenceThreshold != 0.91 || saved.MaxIncidentsToAnalyze != 42 || saved.RecorrelationIntervalMinutes != 9 {
		t.Errorf("updated settings = %+v, want applied changes", saved)
	}
}

func TestAPIHandler_HandleUpdateAggregationSettings_RejectsInvalidJSON(t *testing.T) {
	setupProxyAndAggregationTestDB(t)
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/aggregation", bytes.NewBufferString("{"))
	w := httptest.NewRecorder()

	h.handleUpdateAggregationSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}
