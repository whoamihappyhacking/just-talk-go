package autotype

import (
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// Paste inserts text into the currently focused input field.
func Paste(text string, logger *slog.Logger) error {
	if isWaylandSession() {
		return pasteWayland(text, logger)
	}
	return pasteX11(text, logger)
}

// On X11: sets PRIMARY + CLIPBOARD via xclip, simulates Shift+Insert, restores.
func pasteX11(text string, logger *slog.Logger) error {
	// 1. Save original clipboard
	orig, _ := runClipboard("xclip", "-o", "-selection", "clipboard")

	// 2. Set CLIPBOARD
	if err := pipeToCmd(text, "xclip", "-selection", "clipboard"); err != nil {
		return fmt.Errorf("set clipboard: %w", err)
	}

	// 3. Start xclip for PRIMARY and KEEP IT RUNNING
	primaryCmd := exec.Command("xclip", "-selection", "primary")
	primaryIn, err := primaryCmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("primary pipe: %w", err)
	}
	primaryCmd.Start()
	primaryIn.Write([]byte(text))
	primaryIn.Close()

	// 4. Wait for xclip to be ready, then simulate paste
	time.Sleep(50 * time.Millisecond)

	if err := simulatePaste(); err != nil {
		primaryCmd.Process.Kill()
		primaryCmd.Wait()
		return fmt.Errorf("simulate paste: %w", err)
	}

	// 5. Wait for target app to request selection
	time.Sleep(300 * time.Millisecond)

	// 6. Kill xclip (paste done)
	primaryCmd.Process.Kill()
	primaryCmd.Wait()

	// 7. Restore original clipboard
	if orig != "" && orig != text {
		pipeToCmd(orig, "xclip", "-selection", "clipboard")
	}

	logger.Debug("autotype done", "text_len", len(text))
	return nil
}

func pasteWayland(text string, logger *slog.Logger) error {
	if err := pipeToCmd(text, "wl-copy", "--type", "text/plain;charset=utf-8"); err != nil {
		return fmt.Errorf("set Wayland clipboard: %w", err)
	}
	if err := pipeToCmd(text, "wl-copy", "--primary", "--type", "text/plain;charset=utf-8"); err != nil {
		return fmt.Errorf("set Wayland primary selection: %w", err)
	}

	time.Sleep(50 * time.Millisecond)
	if err := simulatePaste(); err != nil {
		return fmt.Errorf("simulate paste: %w", err)
	}

	logger.Debug("autotype done", "text_len", len(text), "method", pasteMethod())
	return nil
}

func runClipboard(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func pipeToCmd(input string, args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := stdin.Write([]byte(input)); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return err
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}
