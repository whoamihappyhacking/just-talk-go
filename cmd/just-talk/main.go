package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/engine"
	"github.com/c/just-talk-go/hotkey"
	"github.com/c/just-talk-go/internal/doctor"
	"github.com/c/just-talk-go/internal/tui"
	"github.com/c/just-talk-go/plugins"
	"github.com/c/just-talk-go/plugins/overlay"
	"github.com/c/just-talk-go/plugins/voice"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	backend := flag.String("backend", "", "force backend")
	cfgPath := flag.String("config", "", "path to config file")
	debug := flag.Bool("debug", false, "enable debug plugin")
	verbose := flag.Bool("verbose", false, "verbose logging")
	useTUI := flag.Bool("tui", true, "run with terminal UI")
	noTUI := flag.Bool("no-tui", false, "run without terminal UI")
	doctorOnly := flag.Bool("doctor", false, "run startup doctor and exit")
	overlayHelper := flag.Bool("overlay-helper", false, "run macOS overlay helper")
	overlayPosition := flag.String("overlay-position", "top-right", "overlay helper position")
	overlayScale := flag.Float64("overlay-scale", 1.0, "overlay helper scale")
	flag.Parse()
	if *overlayHelper {
		if err := overlay.RunHelper(*overlayPosition, *overlayScale, os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "overlay helper error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *noTUI {
		*useTUI = false
	}

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}

	// Daemon mode: log to stderr + file. TUI mode: file only (stderr corrupts display).
	var logWriter io.Writer
	if *useTUI {
		lf, _ := os.OpenFile("/tmp/just-talk.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if lf != nil {
			logWriter = lf
		} else {
			logWriter = io.Discard
		}
	} else {
		lf, _ := os.OpenFile("/tmp/just-talk.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if lf != nil {
			logWriter = io.MultiWriter(os.Stderr, lf)
		} else {
			logWriter = os.Stderr
		}
	}
	logger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if *backend == "" {
		*backend = os.Getenv("JUST_TALK_BACKEND")
	}
	if *backend != "" {
		os.Setenv("JUST_TALK_BACKEND", *backend)
	}

	report := doctor.Run(cfg, *backend)
	if *doctorOnly || !report.Healthy() {
		report.Print(os.Stderr)
		if report.Healthy() {
			return
		}
		os.Exit(1)
	}

	provider, err := createProvider(*backend)
	if err != nil {
		logger.Error("failed to create provider", "error", err)
		printTroubleshooting(err)
		os.Exit(1)
	}
	logger.Info("provider created", "platform", provider.Info().Platform, "backend", provider.Info().Backend)

	eng := engine.New(provider, cfg, logger)

	if *debug && cfg.Debug.Enabled && !*useTUI {
		eng.LoadPlugin(plugins.NewDebugPlugin())
	}
	eng.LoadPlugin(voice.NewVoicePlugin())
	eng.LoadPlugin(overlay.NewOverlayPlugin())
	if p := config.FindConfig(); p != "" {
		eng.WatchConfig(p)
	}

	if *useTUI {
		runTUI(eng, cfg, *debug)
	} else {
		runDaemon(eng)
	}
}

func runDaemon(eng *engine.Engine) {
	slog.Info("just-talk started — press hotkeys, Ctrl+C to quit")
	if err := eng.Start(true); err != nil && err != context.Canceled {
		slog.Error("engine exited with error", "error", err)
		os.Exit(1)
	}
}

func runTUI(eng *engine.Engine, cfg *config.Config, debug bool) {
	voice.SetupTUILog()
	voice.SetOutput(io.Discard)
	model := tui.New(cfg)
	model.SetDebug(debug)
	model.OnSave = func(c *config.Config) error { return eng.ReloadConfig(c) }
	go func() {
		if err := eng.Start(false); err != nil && err != context.Canceled {
			slog.Error("engine error", "error", err)
		}
	}()
	go func() { model.Update(tui.SetProviderInfo(eng.Provider().Info())) }()
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
	eng.Stop()
}

func createProvider(backend string) (hotkey.Provider, error) {
	if backend == "mock" {
		return hotkey.NewMockProvider(), nil
	}
	return hotkey.NewProvider()
}

func printTroubleshooting(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n\nTroubleshooting:\n", err)
	fmt.Fprintf(os.Stderr, "  X11:      Ensure $DISPLAY is set\n")
	fmt.Fprintf(os.Stderr, "  Wayland:  Add user to 'input' group\n")
	fmt.Fprintf(os.Stderr, "  macOS:    Grant Accessibility permission\n")
}
