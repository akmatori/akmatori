package handlers

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestValidateIntegrationCredentials_Zulip(t *testing.T) {
	full := database.JSONB{"site_url": "https://org.zulipchat.com", "email": "bot@org.zulipchat.com", "api_key": "k"}
	if err := validateIntegrationCredentials(database.MessagingProviderZulip, full); err != nil {
		t.Fatalf("complete credentials: %v", err)
	}
	for _, missing := range []string{"site_url", "email", "api_key"} {
		creds := database.JSONB{}
		for k, v := range full {
			if k != missing {
				creds[k] = v
			}
		}
		creds[missing] = "  "
		err := validateIntegrationCredentials(database.MessagingProviderZulip, creds)
		if err == nil || !strings.Contains(err.Error(), "credentials."+missing) {
			t.Errorf("missing %s: got %v", missing, err)
		}
	}
	if err := validateCanListen(database.MessagingProviderZulip, true); err == nil {
		t.Error("zulip channels must be outbound-only")
	}
	if !shouldMaskCredentialField("api_key") {
		t.Error("the zulip api_key must be masked in responses")
	}
}
