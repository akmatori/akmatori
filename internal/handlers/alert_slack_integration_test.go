package handlers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	internalslack "github.com/akmatori/akmatori/internal/slack"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAlertSlackTestDB(t *testing.T) func() {
	t.Helper()

	prevDB := database.DB
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/test.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}

	if err := db.AutoMigrate(&database.SlackSettings{}, &database.ProxySettings{}, &database.GeneralSettings{}); err != nil {
		t.Fatalf("migrate sqlite db: %v", err)
	}

	database.DB = db
	return func() {
		database.DB = prevDB
	}
}

func TestResolveBaseURL_DBOverridesEnvAndTrimsSlash(t *testing.T) {
	cleanup := setupAlertSlackTestDB(t)
	defer cleanup()

	t.Setenv("AKMATORI_BASE_URL", "https://env.example.com")

	settings := &database.GeneralSettings{BaseURL: "https://db.example.com/"}
	if err := database.DB.Create(settings).Error; err != nil {
		t.Fatalf("create general settings: %v", err)
	}

	if got := resolveBaseURL(); got != "https://db.example.com" {
		t.Fatalf("resolveBaseURL() = %q, want %q", got, "https://db.example.com")
	}
}

func TestResolveBaseURL_UsesEnvWhenDBBaseURLEmpty(t *testing.T) {
	cleanup := setupAlertSlackTestDB(t)
	defer cleanup()

	t.Setenv("AKMATORI_BASE_URL", "https://env.example.com")

	if err := database.DB.Create(&database.GeneralSettings{}).Error; err != nil {
		t.Fatalf("create empty general settings: %v", err)
	}

	if got := resolveBaseURL(); got != "https://env.example.com" {
		t.Fatalf("resolveBaseURL() = %q, want %q", got, "https://env.example.com")
	}
}

func TestResolveBaseURL_FallsBackToLocalhost(t *testing.T) {
	cleanup := setupAlertSlackTestDB(t)
	defer cleanup()

	if err := os.Unsetenv("AKMATORI_BASE_URL"); err != nil {
		t.Fatalf("unset AKMATORI_BASE_URL: %v", err)
	}

	if got := resolveBaseURL(); got != "http://localhost:3000" {
		t.Fatalf("resolveBaseURL() = %q, want %q", got, "http://localhost:3000")
	}
}

// fakeAlertOneShotCaller is a stub OneShotLLMCaller used to drive the
// SlackSummarizer through finalizeSlackMessageBody from the alert flow.
type fakeAlertOneShotCaller struct {
	calls   int
	respond func() (string, error)
}

func (f *fakeAlertOneShotCaller) OneShotLLM(ctx context.Context, llm *services.LLMSettingsForWorker, system, user string, maxTokens int, temperature float64) (string, error) {
	f.calls++
	if f.respond == nil {
		return "", nil
	}
	return f.respond()
}

// TestAlertFinalizeSlackMessageBody_LongResponseSummarized verifies the alert
// flow's final-message construction: an over-budget agent response is
// compressed by the SlackSummarizer and the returned body fits the cap.
func TestAlertFinalizeSlackMessageBody_LongResponseSummarized(t *testing.T) {
	cleanup := setupAlertSlackTestDB(t)
	defer cleanup()

	if err := database.DB.AutoMigrate(&database.LLMSettings{}); err != nil {
		t.Fatalf("migrate llm_settings: %v", err)
	}
	if err := database.DB.Create(&database.LLMSettings{
		Name:     "alert-active",
		Provider: database.LLMProviderAnthropic,
		APIKey:   "sk-test",
		Model:    "claude-sonnet-4",
		Enabled:  true,
		Active:   true,
	}).Error; err != nil {
		t.Fatalf("seed llm_settings: %v", err)
	}

	caller := &fakeAlertOneShotCaller{respond: func() (string, error) {
		return "✅ *Resolved*\nDB primary failed over cleanly.\nView Akmatori UI for details.", nil
	}}
	summarizer := services.NewSlackSummarizer(caller)

	long := strings.Repeat("Detailed log line.\n", 700) +
		"\n[FINAL_RESULT]\nstatus: resolved\nsummary: db failover ok\n[/FINAL_RESULT]"

	got := finalizeSlackMessageBody(context.Background(), summarizer, long, "incident-uuid-1")
	if caller.calls != 1 {
		t.Fatalf("expected exactly 1 LLM call, got %d", caller.calls)
	}
	if !strings.Contains(got, "DB primary failed over cleanly") {
		t.Errorf("expected LLM summary in result, got %q", got)
	}
	if !strings.Contains(got, "/incidents/incident-uuid-1") {
		t.Errorf("expected footer link, got %q", got)
	}
	if len(got) > slackMaxTextBytes {
		t.Errorf("result %d bytes exceeds cap %d", len(got), slackMaxTextBytes)
	}
}

// TestAlertFinalizeSlackMessageBody_ShortResponsePassthrough verifies that
// a short response bypasses the summarizer and is posted as-is + footer.
func TestAlertFinalizeSlackMessageBody_ShortResponsePassthrough(t *testing.T) {
	cleanup := setupAlertSlackTestDB(t)
	defer cleanup()

	if err := database.DB.AutoMigrate(&database.LLMSettings{}); err != nil {
		t.Fatalf("migrate llm_settings: %v", err)
	}

	caller := &fakeAlertOneShotCaller{respond: func() (string, error) {
		t.Fatal("LLM caller must not be invoked for short responses")
		return "", nil
	}}
	summarizer := services.NewSlackSummarizer(caller)

	short := "Investigation complete. Service healthy."
	got := finalizeSlackMessageBody(context.Background(), summarizer, short, "incident-uuid-2")

	if caller.calls != 0 {
		t.Errorf("expected 0 LLM calls for short response, got %d", caller.calls)
	}
	if !strings.Contains(got, "Investigation complete") {
		t.Errorf("expected response body unchanged, got %q", got)
	}
	if !strings.Contains(got, "/incidents/incident-uuid-2") {
		t.Errorf("expected footer link, got %q", got)
	}
}

// TestAlertProgressStreamer_AppendsDuringSimulatedInvestigation simulates the
// alert flow's OnOutput callback piping deltas into the SlackProgressStreamer
// and asserts that AppendStream is called with non-empty status text.
func TestAlertProgressStreamer_AppendsDuringSimulatedInvestigation(t *testing.T) {
	fc := &fakeStreamingClient{}
	streamer := NewSlackProgressStreamer(fc, "C_ALERTS", "1707000001.000100", true, 1*time.Millisecond)

	// Simulate agent OnOutput streaming markers and noise interleaved.
	chunks := []string{
		"\n🛠️ Running: gateway_call\n",
		"Args:\n{}\nOutput:\nrows: 0\n",
		"\n✅ Ran: gateway_call\n",
	}
	for _, c := range chunks {
		streamer.AppendStatus(c)
		time.Sleep(3 * time.Millisecond)
	}
	streamer.Flush()

	calls := fc.snapshotAppend()
	if len(calls) == 0 {
		t.Fatal("expected AppendStream calls for marker-bearing deltas")
	}
	for _, c := range calls {
		if c.text == "" {
			t.Errorf("AppendStream called with empty text: %+v", c)
		}
	}
	if len(fc.snapshotUpdate()) != 0 {
		t.Errorf("UpdateMessage must not be called when isStreaming=true")
	}
}

func TestAlertHandler_IsSlackEnabled_DependsOnSettingsAndClient(t *testing.T) {
	cleanup := setupAlertSlackTestDB(t)
	defer cleanup()

	h := &AlertHandler{slackManager: internalslack.NewManager()}

	if got := h.isSlackEnabled(); got {
		t.Fatal("isSlackEnabled() = true with no settings row, want false")
	}

	settings := &database.SlackSettings{
		BotToken:      "xoxb-test-token",
		SigningSecret: "signing-secret",
		AppToken:      "xapp-test-token",
		AlertsChannel: "C_ALERTS",
		Enabled:       true,
	}
	if err := database.DB.Create(settings).Error; err != nil {
		t.Fatalf("create slack settings: %v", err)
	}

	if got := h.isSlackEnabled(); got {
		t.Fatal("isSlackEnabled() = true without Slack client, want false")
	}

	if err := h.slackManager.Start(context.Background()); err != nil {
		t.Fatalf("start slack manager: %v", err)
	}
	defer h.slackManager.Stop()

	if got := h.isSlackEnabled(); !got {
		t.Fatal("isSlackEnabled() = false with active settings and initialized client, want true")
	}
}
