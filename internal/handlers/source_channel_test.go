package handlers

import (
	"strings"
	"testing"

	slackutil "github.com/akmatori/akmatori/internal/slack"
)

func TestWithSourceChannelPrefixesLabel(t *testing.T) {
	got := withSourceChannel("#csqo_downtime", "User request: check")

	if !strings.HasPrefix(got, "Source channel: #csqo_downtime\n\n") {
		t.Fatalf("expected the channel line first, got %q", got)
	}
	if !strings.HasSuffix(got, "User request: check") {
		t.Fatalf("expected the original task preserved, got %q", got)
	}
}

// An unresolvable channel must not inject a half-built line: the agent matches
// skill rules against the label, and "Source channel: " with nothing after it
// is worse than no line at all.
func TestWithSourceChannelEmptyLabelLeavesTaskUnchanged(t *testing.T) {
	const task = "User request: check"

	if got := withSourceChannel("", task); got != task {
		t.Fatalf("expected the task unchanged, got %q", got)
	}
}

// The Slack and alert paths call ResolveLabel on a resolver that is nil
// whenever the handler was built without a client (every handler-level test,
// and any install where Slack is not configured). That must degrade to "" and
// leave the prompt intact rather than panic mid-investigation.
func TestResolveLabelNilResolverIsSafe(t *testing.T) {
	var r *slackutil.ChannelResolver

	if got := r.ResolveLabel("C0BKMCH9ZHQ"); got != "" {
		t.Fatalf("expected empty label from a nil resolver, got %q", got)
	}
}

func TestResolveLabelClientlessResolverIsSafe(t *testing.T) {
	r := slackutil.NewChannelResolver(nil)

	if got := r.ResolveLabel("C0BKMCH9ZHQ"); got != "" {
		t.Fatalf("expected empty label from a client-less resolver, got %q", got)
	}
}

func TestResolveLabelEmptyChannelID(t *testing.T) {
	r := slackutil.NewChannelResolver(nil)

	if got := r.ResolveLabel("   "); got != "" {
		t.Fatalf("expected empty label for a blank channel ID, got %q", got)
	}
}

// NewSlackHandler must not build a resolver when there is no client, so the
// nil-receiver path above is the one that actually runs in client-less tests.
func TestNewSlackHandlerLeavesResolverNilWithoutClient(t *testing.T) {
	h := NewSlackHandler(nil, nil, nil, nil, nil)

	if h.channelResolver != nil {
		t.Fatal("expected a nil channelResolver when no Slack client is configured")
	}
	if got := h.channelResolver.ResolveLabel("C0BKMCH9ZHQ"); got != "" {
		t.Fatalf("expected empty label, got %q", got)
	}
}
