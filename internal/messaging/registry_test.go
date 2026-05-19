package messaging

import (
	"context"
	"errors"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// fakeProvider is a minimal Provider used to exercise registry routing.
type fakeProvider struct {
	name database.MessagingProvider
}

func (f *fakeProvider) Name() database.MessagingProvider { return f.name }
func (f *fakeProvider) PostMessage(context.Context, *database.Channel, string) (*PostedMessage, error) {
	return &PostedMessage{MessageID: "fake"}, nil
}
func (f *fakeProvider) PostThreadReply(context.Context, *database.Channel, string, string) (*PostedMessage, error) {
	return &PostedMessage{MessageID: "fake-thread"}, nil
}
func (f *fakeProvider) UpdateMessage(context.Context, *database.Channel, string, string) error {
	return nil
}

func TestRegistry_Get_ReturnsProvider(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: database.MessagingProviderSlack})

	p, err := r.Get(database.MessagingProviderSlack)
	if err != nil {
		t.Fatalf("Get(slack) error = %v, want nil", err)
	}
	if p.Name() != database.MessagingProviderSlack {
		t.Errorf("Get(slack) provider name = %q, want %q", p.Name(), database.MessagingProviderSlack)
	}
}

func TestRegistry_Get_UnknownProvider_ReturnsTypedError(t *testing.T) {
	r := NewRegistry()

	_, err := r.Get(database.MessagingProviderTelegram)
	if err == nil {
		t.Fatal("Get(telegram) on empty registry returned nil error, want ErrProviderNotRegistered")
	}
	if !errors.Is(err, ErrProviderNotRegistered) {
		t.Errorf("Get(telegram) error = %v, want errors.Is(err, ErrProviderNotRegistered) to be true", err)
	}
}

func TestRegistry_RegisterReplacesExistingEntry(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: database.MessagingProviderSlack})

	replacement := &fakeProvider{name: database.MessagingProviderSlack}
	r.Register(replacement)

	got, err := r.Get(database.MessagingProviderSlack)
	if err != nil {
		t.Fatalf("Get after replace error = %v", err)
	}
	if got != replacement {
		t.Errorf("Get after replace did not return the most recently registered provider")
	}
}

func TestRegistry_Unregister_RemovesEntry(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: database.MessagingProviderSlack})
	r.Unregister(database.MessagingProviderSlack)

	if _, err := r.Get(database.MessagingProviderSlack); !errors.Is(err, ErrProviderNotRegistered) {
		t.Errorf("Get after Unregister error = %v, want ErrProviderNotRegistered", err)
	}
}

func TestRegistry_List_IsSorted(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: database.MessagingProviderTelegram})
	r.Register(&fakeProvider{name: database.MessagingProviderSlack})

	got := r.List()
	if len(got) != 2 {
		t.Fatalf("List length = %d, want 2", len(got))
	}
	if got[0] != database.MessagingProviderSlack || got[1] != database.MessagingProviderTelegram {
		t.Errorf("List = %v, want [slack telegram] (sorted)", got)
	}
}

func TestTelegramProvider_AllMethodsReturnNotImplemented(t *testing.T) {
	p := NewTelegramProvider()

	if got := p.Name(); got != database.MessagingProviderTelegram {
		t.Errorf("Name = %q, want %q", got, database.MessagingProviderTelegram)
	}
	if _, err := p.PostMessage(context.Background(), &database.Channel{}, "hello"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("PostMessage error = %v, want ErrNotImplemented", err)
	}
	if _, err := p.PostThreadReply(context.Background(), &database.Channel{}, "1", "hello"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("PostThreadReply error = %v, want ErrNotImplemented", err)
	}
	if err := p.UpdateMessage(context.Background(), &database.Channel{}, "1", "hello"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("UpdateMessage error = %v, want ErrNotImplemented", err)
	}
}
