//go:build linux

package voice

import (
	"fmt"
	"os/exec"
	"strings"
)

func pickCommand() (*exec.Cmd, string, error) {
	return pickCommandWithDevice("")
}

func pickCommandWithDevice(device string) (*exec.Cmd, string, error) {
	// ALSA arecord: reliable s16le output, proper format conversion
	al := []string{"-r", "16000", "-f", "S16_LE", "-c", "1", "-t", "raw"}
	// PulseAudio parec: also reliable s16le
	pa := []string{"--rate", "16000", "--format", "s16le", "--channels", "1", "--raw"}
	// PipeWire pw-record: use s16 (NOT s24 — the byte order is incompatible)
	pw := []string{"--rate", "16000", "--format", "s16", "--channels", "1"}

	if device != "" {
		al = append(al, "-D", device)
		pa = append(pa, "--device", device)
		pw = append(pw, "--target", device)
	} else {
		pa = append(pa, "--device", "@DEFAULT_SOURCE@")
	}
	al = append(al, "-")
	pw = append(pw, "-")

	return firstFound([]candidate{
		{"arecord", al},
		{"parec", pa},
		{"pw-record", pw},
	})
}

func errNoBackend(candidates []candidate) error {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.name
	}
	return fmt.Errorf("no recording backend found; install one of: %s", strings.Join(names, ", "))
}

// ListDevices returns available audio input devices.
func ListDevices() ([]string, error) {
	// Try PipeWire
	if pw, err := exec.LookPath("pw-cli"); err == nil {
		cmd := exec.Command(pw, "list-objects", "PipeWire:Interface:Node")
		out, _ := cmd.Output()
		return parsePipeWireDevices(string(out)), nil
	}
	// Try PulseAudio
	if pa, err := exec.LookPath("pactl"); err == nil {
		cmd := exec.Command(pa, "list", "sources", "short")
		out, _ := cmd.Output()
		return parsePulseDevices(string(out)), nil
	}
	// Try ALSA
	if al, err := exec.LookPath("arecord"); err == nil {
		cmd := exec.Command(al, "-L")
		out, _ := cmd.Output()
		return parseALSADevices(string(out)), nil
	}
	return nil, fmt.Errorf("no audio system found (tried pw-cli, pactl, arecord)")
}

func parsePipeWireDevices(output string) []string {
	var devices []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "node.name") && strings.Contains(line, "input") {
			parts := strings.SplitN(line, "\"", 3)
			if len(parts) >= 3 {
				devices = append(devices, parts[1])
			}
		}
	}
	return devices
}

func parsePulseDevices(output string) []string {
	var devices []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			devices = append(devices, fields[1])
		}
	}
	return devices
}

func parseALSADevices(output string) []string {
	var devices []string
	// ALSA -L lists device descriptions; extract hw: or plughw: entries
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "hw:") || strings.HasPrefix(line, "plughw:") ||
			strings.HasPrefix(line, "default") {
			devices = append(devices, line)
		}
	}
	return devices
}
