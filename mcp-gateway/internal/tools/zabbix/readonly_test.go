package zabbix

import (
	"context"
	"log"
	"os"
	"strings"
	"testing"
)

func TestIsReadOnlyMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{"host.get", true},
		{"hostgroup.get", true},
		{"event.get", true},
		{"history.get", true},
		{"Host.Get", true},
		{"apiinfo.version", true},
		{"APIInfo.version", true},

		{"host.delete", false},
		{"host.create", false},
		{"host.update", false},
		{"Host.Delete", false},
		{"user.update", false},
		{"user.login", false},
		{"event.acknowledge", false},
		{"configuration.import", false},
		{"script.execute", false},
		{"", false},
		{"get", false},
		{".get", false},
		{"host.get.delete", false},
		{"host.get ", false},
		{"host.getx", false},
		{"host_delete.get", false},
	}
	for _, tt := range tests {
		if got := IsReadOnlyMethod(tt.method); got != tt.want {
			t.Errorf("IsReadOnlyMethod(%q) = %v, want %v", tt.method, got, tt.want)
		}
	}
}

// TestAPIRequest_RejectsWriteMethods checks that a write method fails before
// any credential lookup or HTTP request: the tool has no config here, so a
// request that went further would fail with a different error.
func TestAPIRequest_RejectsWriteMethods(t *testing.T) {
	tool := NewZabbixTool(log.New(os.Stdout, "test: ", log.LstdFlags), nil)
	defer tool.Stop()

	for _, method := range []string{"host.delete", "user.update", "Host.Create", "script.execute"} {
		_, err := tool.APIRequest(context.Background(), "test-incident", method, nil)
		if err == nil {
			t.Fatalf("APIRequest(%q) succeeded, want rejection", method)
		}
		if !strings.Contains(err.Error(), "read-only methods only") {
			t.Errorf("APIRequest(%q) error = %v, want read-only rejection", method, err)
		}
	}
}
