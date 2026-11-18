package services

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestRenameReservedMCPNamespaces(t *testing.T) {
	db := setupMCPServerTestDB(t)
	// An install from before the built-in github tool: GitHub's MCP server
	// under the prefix "github", and another server already on "github_mcp".
	gh := database.MCPServerConfig{Name: "GitHub MCP", NamespacePrefix: "github", Enabled: true, Transport: database.MCPServerTransportHTTP, URL: "https://api.githubcopilot.com/mcp/"}
	taken := database.MCPServerConfig{Name: "Other", NamespacePrefix: "github_mcp", Enabled: true, Transport: database.MCPServerTransportHTTP, URL: "https://other.example/mcp"}
	keep := database.MCPServerConfig{Name: "Wiki", NamespacePrefix: "ext.wiki", Enabled: true, Transport: database.MCPServerTransportHTTP, URL: "https://wiki.example/mcp"}
	for _, c := range []*database.MCPServerConfig{&gh, &taken, &keep} {
		if err := db.Create(c).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := NewMCPServerServiceWithDB(db).SyncAllManagedTools(); err != nil {
		t.Fatal(err)
	}
	var inst database.ToolInstance
	db.Joins("JOIN tool_types ON tool_types.id = tool_instances.tool_type_id").Where("tool_types.name = ?", "github").First(&inst)
	db.Create(&database.HTTPConnector{ToolTypeName: "gitea", Enabled: true, Tools: database.JSONB{}})

	changed := RenameReservedMCPNamespaces(db)
	if len(changed) != 2 {
		t.Fatalf("changed %v, want the github MCP server renamed and the gitea HTTP connector disabled", changed)
	}
	var got database.MCPServerConfig
	db.First(&got, gh.ID)
	if got.NamespacePrefix != "github_mcp_2" || !got.Enabled {
		t.Fatalf("prefix %q enabled %v, want github_mcp_2 and still enabled", got.NamespacePrefix, got.Enabled)
	}
	var tt database.ToolType
	if err := db.Where("name = ? AND kind = ?", "github_mcp_2", database.ToolTypeKindMCP).First(&tt).Error; err != nil {
		t.Fatalf("the managed tool type must move with the prefix: %v", err)
	}
	var moved database.ToolInstance
	if err := db.Where("tool_type_id = ?", tt.ID).First(&moved).Error; err != nil || moved.ID != inst.ID {
		t.Fatalf("the managed tool instance must stay the same row: %v (%d vs %d)", err, moved.ID, inst.ID)
	}
	var n int64
	db.Model(&database.ToolType{}).Where("name = ?", "github").Count(&n)
	if n != 0 {
		t.Fatal("no tool type may keep the name github, or the built-in seed would skip it")
	}
	var other database.MCPServerConfig
	db.First(&other, keep.ID)
	if other.NamespacePrefix != "ext.wiki" {
		t.Error("an unrelated MCP server was renamed")
	}
	var hc database.HTTPConnector
	db.Where("tool_type_name = ?", "gitea").First(&hc)
	if hc.Enabled {
		t.Error("an HTTP connector named gitea must be disabled")
	}
	if again := RenameReservedMCPNamespaces(db); len(again) != 0 {
		t.Errorf("a second start changed %v", again)
	}
}
