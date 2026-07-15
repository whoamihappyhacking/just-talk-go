package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
	installOnly := flag.Bool("install", false, "install just-talk for the current user")
	overlayHelper := flag.Bool("overlay-helper", false, "run macOS overlay helper")
	overlayPosition := flag.String("overlay-position", "top-right", "overlay helper position")
	overlayScale := flag.Float64("overlay-scale", 1.0, "overlay helper scale")
	flag.Parse()
	if *installOnly {
		if err := installSelf(); err != nil {
			fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
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
	logPath := defaultLogPath()
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	var logWriter io.Writer
	if *useTUI {
		lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if lf != nil {
			logWriter = lf
		} else {
			logWriter = io.Discard
		}
	} else {
		lf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
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
	fmt.Fprintf(os.Stderr, "  Windows:  Check microphone privacy settings\n")
}

func installSelf() error {
	targetDir, err := installDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", targetDir, err)
	}

	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(src); err == nil {
		src = resolved
	}
	name := "just-talk"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(targetDir, name)
	if samePath(src, target) {
		fmt.Fprintf(os.Stdout, "just-talk is already installed at %s\n", target)
		printInstallPathNote(targetDir)
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open current executable %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(targetDir, ".just-talk-*")
	if err != nil {
		return fmt.Errorf("create temporary installer file: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy executable: %w", err)
	}
	if err := tmp.Chmod(0755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set executable mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary installer file: %w", err)
	}
	if err := replaceInstalledFile(tmpName, target); err != nil {
		return fmt.Errorf("install to %s: %w", target, err)
	}
	ok = true

	fmt.Fprintf(os.Stdout, "Installed just-talk to %s\n", target)
	printInstallPathNote(targetDir)
	return nil
}

func installDir() (string, error) {
	if runtime.GOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if base == "" {
			var err error
			base, err = os.UserCacheDir()
			if err != nil {
				return "", fmt.Errorf("find local application data directory: %w", err)
			}
		}
		return filepath.Join(base, "Programs", "Just Talk"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func replaceInstalledFile(source, target string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(source, target)
	}
	backup := target + ".old"
	_ = os.Remove(backup)
	hadTarget := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		hadTarget = true
	}
	if err := os.Rename(source, target); err != nil {
		if hadTarget {
			_ = os.Rename(backup, target)
		}
		return err
	}
	if hadTarget {
		_ = os.Remove(backup)
	}
	return nil
}

func defaultLogPath() string {
	if runtime.GOOS == "windows" {
		if dir, err := os.UserCacheDir(); err == nil {
			return filepath.Join(dir, "just-talk", "just-talk.log")
		}
	}
	return "/tmp/just-talk.log"
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil {
		a = absA
	}
	if errB == nil {
		b = absB
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func printInstallPathNote(dir string) {
	if !pathContains(dir) {
		fmt.Fprintf(os.Stdout, "Note: %s is not in PATH. Add it to your shell profile to run just-talk directly.\n", dir)
	}
}

func pathContains(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if samePath(p, dir) {
			return true
		}
		if rel, err := filepath.Rel(p, dir); err == nil && rel == "." {
			return true
		}
		if strings.TrimRight(p, string(os.PathSeparator)) == strings.TrimRight(dir, string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
