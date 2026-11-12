package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

func seedAuthzTools(t *testing.T, db *gorm.DB) map[string]database.ToolInstance {
	t.Helper()
	out := map[string]database.ToolInstance{}
	types := map[string]uint{}
	for _, name := range []string{"ssh", "zabbix", "incidents", "proposals", "events", "github_ext"} {
		tt := database.ToolType{Name: name}
		if name == "github_ext" {
			tt.Kind = database.ToolTypeKindMCP
		}
		if err := db.Create(&tt).Error; err != nil {
			t.Fatal(err)
		}
		types[name] = tt.ID
	}
	// Created out of ID order on purpose: zabbix first, then ssh.
	for _, ti := range []database.ToolInstance{
		{ToolTypeID: types["zabbix"], Name: "Zabbix", LogicalName: "prod-zabbix", Enabled: true},
		{ToolTypeID: types["ssh"], Name: "SSH A", LogicalName: "ssh-a", Enabled: true},
		{ToolTypeID: types["ssh"], Name: "SSH off", LogicalName: "ssh-off", Enabled: true},
		{ToolTypeID: types["incidents"], Name: "Incidents", LogicalName: "incidents", Enabled: true},
		{ToolTypeID: types["proposals"], Name: "Proposals", LogicalName: "proposals", Enabled: true},
		{ToolTypeID: types["events"], Name: "Events", LogicalName: "events", Enabled: true},
		{ToolTypeID: types["github_ext"], Name: "GitHub", LogicalName: "github", Enabled: true},
	} {
		ti := ti
		if err := db.Create(&ti).Error; err != nil {
			t.Fatal(err)
		}
		out[ti.LogicalName] = ti
	}
	if err := db.Model(&database.ToolInstance{}).Where("logical_name = ?", "ssh-off").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	return out
}

func entryTypes(entries []ToolAllowlistEntry) (instances []string, builtins map[string]bool) {
	builtins = map[string]bool{}
	for _, e := range entries {
		if e.InstanceID == 0 {
			builtins[e.ToolType] = true
			continue
		}
		instances = append(instances, e.LogicalName)
	}
	return
}

func TestIncidentRunAllowlist(t *testing.T) {
	db := setupSkillTestDB(t)
	tools := seedAuthzTools(t, db)

	entries := IncidentRunAllowlist(db)
	instances, builtins := entryTypes(entries)
	want := []string{"prod-zabbix", "ssh-a", "github"}
	if len(instances) != len(want) {
		t.Fatalf("connector instances = %v, want %v", instances, want)
	}
	for i := range want {
		if instances[i] != want[i] {
			t.Errorf("instance %d = %s, want %s (ordered by ID)", i, instances[i], want[i])
		}
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].InstanceID != 0 && entries[i-1].InstanceID > entries[i].InstanceID {
			t.Errorf("not ordered by instance ID: %+v", entries)
		}
	}
	if !builtins["events"] || !builtins["incidents"] {
		t.Errorf("incident built-ins missing: %v", builtins)
	}
	if builtins["proposals"] {
		t.Error("proposals must never reach incident runs")
	}
	if !builtins["runbooks"] || !builtins["memory"] {
		t.Error("incident runs need the runbooks and memory built-ins")
	}
	for _, e := range entries {
		if e.LogicalName == "ssh-off" {
			t.Error("disabled instance in allowlist")
		}
		if e.InstanceID == tools["incidents"].ID {
			t.Error("built-in instances must be type-only entries")
		}
	}
}

func TestIncidentRunAllowlist_NoConnectors(t *testing.T) {
	db := setupSkillTestDB(t)
	entries := IncidentRunAllowlist(db)
	if entries == nil {
		t.Fatal("allowlist must be non-nil")
	}
	instances, builtins := entryTypes(entries)
	if len(instances) != 0 || !builtins["events"] {
		t.Errorf("got %v %v", instances, builtins)
	}
}

func TestIncidentRunAllowlist_FailsClosed(t *testing.T) {
	db := setupSkillTestDB(t)
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()
	entries := IncidentRunAllowlist(db)
	if entries == nil || len(entries) != 0 {
		t.Errorf("lookup error must yield a non-nil empty allowlist, got %v", entries)
	}
}

func TestCronAllowlist(t *testing.T) {
	db := setupSkillTestDB(t)
	seedAuthzTools(t, db)

	on := CronAllowlist(db, &database.CronJob{ToolsEnabled: true, BuiltinTools: database.StringList{"events"}})
	instances, builtins := entryTypes(on)
	if len(instances) != 3 || !builtins["events"] || builtins["incidents"] {
		t.Errorf("tools_enabled: %v %v", instances, builtins)
	}

	off := CronAllowlist(db, &database.CronJob{BuiltinTools: database.StringList{"events", "memory", "incidents", "proposals"}})
	instances, builtins = entryTypes(off)
	if len(instances) != 0 {
		t.Errorf("tools_enabled=false leaked connectors: %v", instances)
	}
	if !builtins["incidents"] || !builtins["proposals"] || !builtins["memory"] || builtins["runbooks"] {
		t.Errorf("builtins = %v", builtins)
	}

	none := CronAllowlist(db, &database.CronJob{BuiltinTools: database.StringList{}})
	if none == nil || len(none) != 0 {
		t.Errorf("no tools must be a non-nil empty allowlist, got %v", none)
	}
}

// seedTelegram adds a Telegram integration with one chat. It returns the
// integration and the chat.
func seedTelegram(t *testing.T, db *gorm.DB, token string) (database.Integration, database.Channel) {
	t.Helper()
	in := database.Integration{UUID: "tg-" + token, Provider: database.MessagingProviderTelegram, Name: "TG",
		Credentials: database.JSONB{"bot_token": token}, Enabled: true}
	if err := db.Create(&in).Error; err != nil {
		t.Fatal(err)
	}
	ch := database.Channel{UUID: "ch-" + token, IntegrationID: in.ID, ExternalID: "-1001", DisplayName: "oncall", CanPost: true, Enabled: true}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	return in, ch
}

func TestTelegramToolAvailable(t *testing.T) {
	db := setupSkillTestDB(t)
	if TelegramToolAvailable(db) {
		t.Fatal("no integration: must be unavailable")
	}
	_, ch := seedTelegram(t, db, "123:abc")
	if !TelegramToolAvailable(db) {
		t.Fatal("integration with token and chat: must be available")
	}

	db.Model(&database.Channel{}).Where("id = ?", ch.ID).Update("can_post", false)
	if TelegramToolAvailable(db) {
		t.Error("chat that cannot post: must be unavailable")
	}
	db.Model(&database.Channel{}).Where("id = ?", ch.ID).Updates(map[string]any{"can_post": true, "enabled": false})
	if TelegramToolAvailable(db) {
		t.Error("disabled chat: must be unavailable")
	}
	db.Model(&database.Channel{}).Where("id = ?", ch.ID).Update("enabled", true)
	db.Model(&database.Integration{}).Where("1 = 1").Update("enabled", false)
	if TelegramToolAvailable(db) {
		t.Error("disabled integration: must be unavailable")
	}
	db.Model(&database.Integration{}).Where("1 = 1").Updates(map[string]any{"enabled": true, "credentials": database.JSONB{"bot_token": ""}})
	if TelegramToolAvailable(db) {
		t.Error("integration without token: must be unavailable")
	}

	// A Slack chat never makes the telegram tool available.
	db2 := setupSkillTestDB(t)
	sl := database.Integration{UUID: "sl", Provider: database.MessagingProviderSlack, Name: "S", Credentials: database.JSONB{"bot_token": "xoxb"}, Enabled: true}
	db2.Create(&sl)
	db2.Create(&database.Channel{UUID: "c", IntegrationID: sl.ID, ExternalID: "C1", CanPost: true, Enabled: true})
	if TelegramToolAvailable(db2) {
		t.Error("slack integration: must be unavailable")
	}
}

func TestAllowlists_Telegram(t *testing.T) {
	db := setupSkillTestDB(t)
	seedAuthzTools(t, db)

	_, builtins := entryTypes(IncidentRunAllowlist(db))
	if builtins[MessagingToolTelegram] {
		t.Fatal("telegram granted without a Telegram connector")
	}

	seedTelegram(t, db, "123:abc")
	_, builtins = entryTypes(IncidentRunAllowlist(db))
	if !builtins[MessagingToolTelegram] {
		t.Error("incident runs must get telegram when a chat exists")
	}
	_, builtins = entryTypes(CronAllowlist(db, &database.CronJob{ToolsEnabled: true}))
	if !builtins[MessagingToolTelegram] {
		t.Error("tools_enabled crons must get telegram")
	}
	_, builtins = entryTypes(CronAllowlist(db, &database.CronJob{BuiltinTools: database.StringList{"events"}}))
	if builtins[MessagingToolTelegram] {
		t.Error("tools_enabled=false crons must not get telegram")
	}
	_, builtins = entryTypes(ProposalChatAllowlist())
	if builtins[MessagingToolTelegram] {
		t.Error("proposal chat must not get telegram")
	}
}

func TestReservedToolNamespaces_Telegram(t *testing.T) {
	if !isReservedToolNamespace(MessagingToolTelegram) {
		t.Error("telegram must be reserved for HTTP connectors and MCP servers")
	}
}

func TestNormalizeBuiltinTools(t *testing.T) {
	got, _, ok := NormalizeBuiltinTools([]string{"proposals", "events", "memory", "events"})
	if !ok || len(got) != 3 || got[0] != "events" || got[1] != "memory" || got[2] != "proposals" {
		t.Errorf("got %v %v", got, ok)
	}
	if _, bad, ok := NormalizeBuiltinTools([]string{"events", "ssh"}); ok || bad != "ssh" {
		t.Errorf("ssh must be refused, got bad=%q ok=%v", bad, ok)
	}
}

func TestConnectedToolsSkillMd_Telegram(t *testing.T) {
	without := generateConnectedToolsSkillMd(nil, nil, false)
	if strings.Contains(without, "telegram.send_message") {
		t.Error("telegram section must be absent without a Telegram chat")
	}
	if !strings.Contains(without, "No connectors are enabled") {
		t.Error("no connectors: the hint must stay")
	}
	with := generateConnectedToolsSkillMd(nil, nil, true)
	if !strings.Contains(with, "## telegram") || !strings.Contains(with, `"telegram.send_message"`) || !strings.Contains(with, `"telegram.list_chats"`) {
		t.Errorf("telegram section missing:\n%s", with)
	}
	if strings.Contains(with, "No connectors are enabled") {
		t.Error("telegram is a connector tool: the no-connectors hint must go")
	}
	if strings.Index(with, "## telegram") > strings.Index(with, "## Built-in tools") {
		t.Error("telegram belongs with the connector tools, before the built-ins")
	}
}
