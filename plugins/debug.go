package plugins

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/hotkey"
)

// DebugPlugin registers hotkeys from the config and prints events.
// It implements engine.Reloader for config hot-reload.
type DebugPlugin struct {
	env       engine.PluginEnv
	logger    *slog.Logger
	callbacks map[hotkey.Combo]func(hotkey.Event)
}

// NewDebugPlugin creates a DebugPlugin.
func NewDebugPlugin() *DebugPlugin {
	return &DebugPlugin{
		callbacks: make(map[hotkey.Combo]func(hotkey.Event)),
	}
}

func (p *DebugPlugin) Name() string    { return "debug" }
func (p *DebugPlugin) Version() string { return "0.1.0" }

func (p *DebugPlugin) Init(env engine.PluginEnv) error {
	p.env = env
	p.logger = env.Logger()
	return p.registerFromConfig(env.Config())
}

func (p *DebugPlugin) Start(ctx context.Context) error {
	p.logger.Info("debug plugin started — listening for hotkeys")
	<-ctx.Done()
	return ctx.Err()
}

func (p *DebugPlugin) Stop() error {
	p.logger.Info("debug plugin stopped")
	return nil
}

// OnConfigReload handles hot-reload by re-registering hotkeys.
func (p *DebugPlugin) OnConfigReload(cfg *config.Config) error {
	p.logger.Info("reloading hotkeys from config")
	return p.registerFromConfig(cfg)
}

// registerFromConfig parses hotkeys from config and registers them.
// It unregisters any previously registered hotkeys first.
func (p *DebugPlugin) registerFromConfig(cfg *config.Config) error {
	// Unregister old hotkeys
	for combo := range p.callbacks {
		if err := p.env.UnregisterHotkey(combo); err != nil {
			p.logger.Warn("failed to unregister old hotkey", "combo", combo, "error", err)
		}
	}
	p.callbacks = make(map[hotkey.Combo]func(hotkey.Event))

	// Only register hotkeys explicitly configured by the user
	hotkeyStrs := cfg.Debug.Hotkeys
	if len(hotkeyStrs) == 0 {
		return nil
	}

	for _, s := range hotkeyStrs {
		combo, err := config.ParseHotkey(s)
		if err != nil {
			p.logger.Error("invalid hotkey in config", "key", s, "error", err)
			continue
		}

		c := combo // capture
		cb := func(evt hotkey.Event) {
			fmt.Printf("🔔 HOTKEY: %-40s | Type: %-8s | Time: %s\n",
				evt.Combo, evt.Type, evt.Time.Format("15:04:05.000"))
		}

		if err := p.env.RegisterHotkey(c, cb); err != nil {
			p.logger.Error("failed to register hotkey", "combo", c, "error", err)
			continue
		}

		p.callbacks[c] = cb
		p.logger.Info("registered hotkey", "combo", c)
	}

	return nil
}
