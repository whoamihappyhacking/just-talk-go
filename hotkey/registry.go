package hotkey

import (
	"context"
	"fmt"
	"sync"
)

// Registry manages multiple hotkey registrations on a single Provider.
//
// It handles the fan-out from per-combo event channels to callbacks,
// and ensures thread-safe registration/unregistration while the provider
// is running.
type Registry struct {
	provider Provider

	mu          sync.RWMutex
	handlers    map[Combo]handlerEntry
	dispatchCtx context.Context
	started     bool
}

type handlerEntry struct {
	handler func(Event)
	ch      <-chan Event
	cancel  context.CancelFunc
}

// NewRegistry creates a new Registry backed by the given Provider.
func NewRegistry(provider Provider) *Registry {
	return &Registry{
		provider: provider,
		handlers: make(map[Combo]handlerEntry),
	}
}

// Register registers a hotkey combo with a callback handler.
// The handler is called from the provider's event loop goroutine,
// so it should not block for long.
//
// Returns an error if the combo is already registered.
func (r *Registry) Register(combo Combo, handler func(Event)) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[combo]; exists {
		return fmt.Errorf("hotkey %s is already registered", combo)
	}

	ch, err := r.provider.Register(combo)
	if err != nil {
		return fmt.Errorf("register hotkey %s: %w", combo, err)
	}

	r.handlers[combo] = handlerEntry{
		handler: handler,
		ch:      ch,
	}

	// Start dispatch goroutine for late-registered handlers
	if r.started {
		go r.dispatch(r.dispatchCtx, r.handlers[combo])
	}

	return nil
}

// Unregister removes a previously registered hotkey combo.
// Returns an error if the combo is not registered.
func (r *Registry) Unregister(combo Combo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.handlers[combo]
	if !exists {
		return fmt.Errorf("hotkey %s is not registered", combo)
	}

	if err := r.provider.Unregister(combo); err != nil {
		return fmt.Errorf("unregister hotkey %s: %w", combo, err)
	}

	delete(r.handlers, combo)
	_ = entry // channel is closed by provider.Unregister

	return nil
}

// Start begins listening for hotkey events. It starts the provider and
// fans out events from each combo's channel to the registered handler.
//
// This call blocks until ctx is cancelled or Stop() is called.
func (r *Registry) Start(ctx context.Context) error {
	// Start the fan-out dispatcher
	var cancel context.CancelFunc
	r.dispatchCtx, cancel = context.WithCancel(ctx)
	defer cancel()

	r.mu.Lock()
	r.started = true
	r.mu.Unlock()

	// Launch per-handler dispatch goroutines
	r.mu.RLock()
	for _, entry := range r.handlers {
		go r.dispatch(r.dispatchCtx, entry)
	}
	r.mu.RUnlock()

	// Run the provider (this blocks)
	err := r.provider.Start(ctx)

	r.mu.Lock()
	r.started = false
	r.mu.Unlock()

	return err
}

// Stop signals the registry to stop listening.
func (r *Registry) Stop() error {
	return r.provider.Stop()
}

// Info returns the underlying provider's info.
func (r *Registry) Info() ProviderInfo {
	return r.provider.Info()
}

// Provider returns the underlying provider.
func (r *Registry) Provider() Provider {
	return r.provider
}

// dispatch reads events from a combo's channel and calls the handler.
func (r *Registry) dispatch(ctx context.Context, entry handlerEntry) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-entry.ch:
			if !ok {
				// Channel closed (unregistered)
				return
			}
			entry.handler(event)
		}
	}
}
