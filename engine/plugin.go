// Package engine provides the application lifecycle manager and plugin system.
//
// The Engine coordinates the hotkey provider, manages plugin loading,
// and handles OS signal-based shutdown.
package engine

import (
	"context"
	"log/slog"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/hotkey"
)

// Plugin is the interface for all engine plugins.
//
// Plugins receive lifecycle events from the Engine and can register
// their own hotkeys via the PluginEnv.
//
// Implementation is intentionally simple: compile-time Go plugins.
// Process-external plugins (via hashicorp/go-plugin) may be added later.
type Plugin interface {
	// Name returns a human-readable plugin name (used in logs).
	Name() string

	// Version returns the plugin version string.
	Version() string

	// Init is called once after the plugin is loaded.
	// The plugin receives a PluginEnv for registering hotkeys and logging.
	Init(env PluginEnv) error

	// Start is called when the engine starts. It runs in its own goroutine.
	// The plugin should perform its main work here and return when ctx is cancelled.
	Start(ctx context.Context) error

	// Stop is called when the engine is shutting down.
	// The plugin should clean up resources. Called after context cancellation.
	Stop() error
}

// PluginEnv provides the environment a plugin runs in.
type PluginEnv interface {
	// RegisterHotkey registers a hotkey combo with a handler callback.
	// The handler is called from the hotkey event loop; it must not block.
	RegisterHotkey(combo hotkey.Combo, handler func(hotkey.Event)) error

	// UnregisterHotkey removes a previously registered hotkey.
	UnregisterHotkey(combo hotkey.Combo) error

	// Logger returns the plugin's logger (pre-configured with plugin name).
	Logger() *slog.Logger

	// Config returns the application configuration.
	Config() *config.Config

	// Engine returns a reference to the engine (for advanced use).
	Engine() *Engine
}

// Reloader is an optional interface that plugins can implement to
// receive notifications when the configuration file is hot-reloaded.
type Reloader interface {
	OnConfigReload(cfg *config.Config) error
}

// basePlugin provides a default implementation for optional methods.
// Plugins can embed this to avoid implementing every method.
type basePlugin struct{}

func (basePlugin) Init(PluginEnv) error { return nil }
func (basePlugin) Stop() error          { return nil }
