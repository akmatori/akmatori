package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

type fakeGatewayTools struct {
	tools map[string][]GatewayTool
	err   error
	calls int
}

func (f *fakeGatewayTools) NamespaceTools(_ context.Context, ns string) ([]GatewayTool, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.tools[ns], nil
}

func githubTool() GatewayTool {
	var gt GatewayTool
	gt.Name = "github_ext.search_issues"
	gt.Description = "Search issues in a repository.\nLonger text."
	gt.InputSchema.Properties = map[string]GatewayToolParam{
		"query":        {Type: "string"},
		"repo":         {Type: "string"},
		"logical_name": {Type: "string"},
	}
	gt.InputSchema.Required = []string{"query"}
	return gt
}

func readConnectedTools(t *testing.T, svc *SkillService) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(svc.GetSkillDir(ConnectedToolsSkillName), "SKILL.md"))
	if err != nil {
		t.Fatalf("read connected-tools: %v", err)
	}
	return string(b)
}

func TestRegenerateConnectedToolsSkill(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)
	seedAuthzTools(t, db)
	fake := &fakeGatewayTools{tools: map[string][]GatewayTool{"github_ext": {githubTool()}}}
	svc.SetGatewayToolLister(fake)

	if err := svc.RegenerateConnectedToolsSkill(); err != nil {
		t.Fatal(err)
	}
	md := readConnectedTools(t, svc)
	for _, want := range []string{
		"name: connected-tools",
		"## ssh", `### SSH A (logical_name: "ssh-a", type: ssh)`,
		"## zabbix", `"prod-zabbix"`,
		"## github_ext", "`github_ext.search_issues(query*: string, repo: string)` — Search issues in a repository.",
		"## Built-in tools", "### events", "### incidents",
		"refused by the gateway",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("connected-tools missing %q\n%s", want, md)
		}
	}
	for _, bad := range []string{"ssh-off", "### proposals", "logical_name: string"} {
		if strings.Contains(md, bad) {
			t.Errorf("connected-tools must not contain %q", bad)
		}
	}
	if fake.calls != 1 {
		t.Errorf("gateway asked %d times, want once (only the MCP namespace)", fake.calls)
	}

	// A disabled instance disappears on the next render.
	if err := db.Model(&database.ToolInstance{}).Where("logical_name = ?", "prod-zabbix").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RegenerateSkillMd(ConnectedToolsSkillName); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readConnectedTools(t, svc), "prod-zabbix") {
		t.Error("disabled instance still listed")
	}
}

func TestRegenerateConnectedToolsSkill_GatewayDown(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)
	seedAuthzTools(t, db)
	svc.SetGatewayToolLister(&fakeGatewayTools{err: errors.New("down")})
	if err := svc.RegenerateConnectedToolsSkill(); err != nil {
		t.Fatal(err)
	}
	md := readConnectedTools(t, svc)
	if !strings.Contains(md, `list_tools_for_tool_type("github_ext")`) {
		t.Errorf("MCP fallback hint missing:\n%s", md)
	}
}

func TestRegenerateConnectedToolsSkill_SeveralInstancesNote(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)
	tools := seedAuthzTools(t, db)
	if err := db.Create(&database.ToolInstance{ToolTypeID: tools["ssh-a"].ToolTypeID, Name: "SSH B", LogicalName: "ssh-b", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RegenerateConnectedToolsSkill(); err != nil {
		t.Fatal(err)
	}
	if md := readConnectedTools(t, svc); !strings.Contains(md, "2 instances: `ssh-a`, `ssh-b`") {
		t.Errorf("several-instances note missing:\n%s", md)
	}
}

func TestEnsureConnectedToolsSkill(t *testing.T) {
	db := setupSkillTestDB(t)
	svc := newTestSkillService(t, db)

	// An operator skill already uses the name.
	if err := db.Create(&database.Skill{Name: ConnectedToolsSkillName, Description: "mine", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	dir := svc.GetSkillDir(ConnectedToolsSkillName)
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: connected-tools\ndescription: mine\n---\n\nmy prompt"), 0644)

	if err := svc.EnsureConnectedToolsSkill(); err != nil {
		t.Fatal(err)
	}
	var renamed database.Skill
	if err := db.Where("name = ?", "connected-tools-1").First(&renamed).Error; err != nil {
		t.Fatalf("operator skill not renamed: %v", err)
	}
	if renamed.IsSystem {
		t.Error("renamed operator skill must stay non-system")
	}
	if _, err := os.Stat(filepath.Join(svc.GetSkillDir("connected-tools-1"), "SKILL.md")); err != nil {
		t.Errorf("operator skill directory not moved: %v", err)
	}
	var sys database.Skill
	if err := db.Where("name = ?", ConnectedToolsSkillName).First(&sys).Error; err != nil || !sys.IsSystem || !sys.Enabled {
		t.Fatalf("system row: %+v %v", sys, err)
	}

	// Idempotent; the filesystem import pass does not duplicate it.
	if err := svc.EnsureConnectedToolsSkill(); err != nil {
		t.Fatal(err)
	}
	if err := svc.RegenerateAllSkillMds(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncSkillsFromFilesystem(); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&database.Skill{}).Where("name = ?", ConnectedToolsSkillName).Count(&n)
	if n != 1 {
		t.Errorf("connected-tools rows = %d, want 1", n)
	}

	// Worker sees it; operators cannot edit or delete it.
	names := svc.GetEnabledSkillNames()
	found := false
	for _, nm := range names {
		found = found || nm == ConnectedToolsSkillName
	}
	if !found {
		t.Errorf("GetEnabledSkillNames = %v, want connected-tools included", names)
	}
	if err := svc.DeleteSkill(ConnectedToolsSkillName); err == nil {
		t.Error("delete of the system skill must fail")
	}
	if _, err := svc.UpdateSkill(ConnectedToolsSkillName, "x", "", false); !errors.Is(err, ErrGeneratedSkill) {
		t.Errorf("update err = %v, want ErrGeneratedSkill", err)
	}
	if err := svc.UpdateSkillPrompt(ConnectedToolsSkillName, "x"); !errors.Is(err, ErrGeneratedSkill) {
		t.Errorf("prompt update err = %v, want ErrGeneratedSkill", err)
	}
}
