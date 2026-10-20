// Package messaging defines the cross-provider abstraction for outbound
// messaging triggered by alerts, cron jobs, and the workspace default.
//
// The Provider interface is intentionally narrow: it is the surface area that
// callers need to address a Channel without knowing which underlying SaaS
// (Slack, Zulip, Telegram, ...) backs it. Provider implementations live in this
// package; consumers (handlers, services) depend on the interface via the
// ProviderRegistry that internal/services/interfaces.go exposes.
package messaging

import (
	"context"
	"errors"

	"github.com/akmatori/akmatori/internal/database"
)

// ErrNotImplemented is returned by provider stubs whose underlying SaaS
// support has not yet landed. The error is wrapped through PostMessage,
// PostThreadReply, and UpdateMessage so callers can distinguish a configured
// provider returning a transport error from a known-absent provider.
var ErrNotImplemented = errors.New("messaging provider not implemented")

// ErrProviderNotRegistered is returned by ProviderRegistry.Get when the
// requested name is unknown. Callers should treat this as a programming or
// configuration error and degrade gracefully (do not crash the API).
var ErrProviderNotRegistered = errors.New("messaging provider not registered")

// PostedMessage is the response shape returned by all Provider write methods.
// MessageID is provider-defined: Slack uses the message timestamp (ts), other
// providers use their own message identifier (e.g. Zulip message ID).
// Callers should treat it as an opaque string when threading replies or
// updating the message.
type PostedMessage struct {
	MessageID string
}

// Provider is the cross-SaaS abstraction every messaging integration must
// implement. The interface is deliberately limited to the methods that
// outbound alert posting, cron-job posting, and provider-thread replies need;
// listener / read-side concerns remain provider-specific.
type Provider interface {
	// Name returns the canonical provider identifier (e.g. "slack",
	// "telegram"). The name MUST match the value stored on
	// Integration.Provider for ProviderRegistry routing to work.
	Name() database.MessagingProvider

	// PostMessage posts a top-level message to the given Channel and
	// returns a PostedMessage whose MessageID can be used as a thread root
	// or as an update target.
	PostMessage(ctx context.Context, channel *database.Channel, text string) (*PostedMessage, error)

	// PostThreadReply posts a message into an existing thread anchored by
	// parentMessageID (the value previously returned in PostedMessage from
	// PostMessage). Providers without a native thread concept may emulate
	// it (e.g. quote-reply) or return ErrNotImplemented.
	PostThreadReply(ctx context.Context, channel *database.Channel, parentMessageID, text string) (*PostedMessage, error)

	// UpdateMessage rewrites the body of an already-posted message. Used
	// for streaming progress banners and final summaries. Providers that
	// do not support edit-in-place must return ErrNotImplemented so the
	// caller can fall back to threaded replies.
	UpdateMessage(ctx context.Context, channel *database.Channel, messageID, text string) error
}
