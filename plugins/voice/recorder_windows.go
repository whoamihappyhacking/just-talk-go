//go:build windows

package voice

import (
	"fmt"
	"os/exec"
	"strings"
)

func pickCommand() (*exec.Cmd, string, error) {
	return firstFound([]candidate{
		// ffmpeg with dshow (most reliable on Windows)
		// Uses default audio input device
		{"ffmpeg", []string{
			"-f", "dshow", "-i", "audio=default",
			"-ar", "16000", "-ac", "1", "-f", "s16le",
			"-loglevel", "quiet", "-",
		}},
		// sox with waveaudio driver
		{"sox", []string{
			"-t", "waveaudio", "default",
			"-r", "16000", "-b", "16", "-c", "1",
			"-t", "raw", "-",
		}},
	})
}

func pickCommandWithDevice(device string) (*exec.Cmd, string, error) {
	if device == "" {
		return pickCommand()
	}
	return firstFound([]candidate{
		{"ffmpeg", []string{
			"-f", "dshow", "-i", "audio=" + device,
			"-ar", "16000", "-ac", "1", "-f", "s16le",
			"-loglevel", "quiet", "-",
		}},
		{"sox", []string{
			"-t", "waveaudio", device,
			"-r", "16000", "-b", "16", "-c", "1",
			"-t", "raw", "-",
		}},
	})
}

func errNoBackend(candidates []candidate) error {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.name
	}
	return fmt.Errorf("no recording backend found; install one of: %s", strings.Join(names, ", "))
}

// ListDevices returns available audio input devices on Windows using ffmpeg.
func ListDevices() ([]string, error) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found (needed to list devices)")
	}
	cmd := exec.Command(ff, "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	out, _ := cmd.CombinedOutput()
	return parseDeviceList(string(out)), nil
}

func parseDeviceList(output string) []string {
	var devices []string
	lines := strings.Split(output, "\n")
	inAudio := false
	for _, line := range lines {
		if strings.Contains(line, "DirectShow audio") {
			inAudio = true
			continue
		}
		if inAudio && strings.Contains(line, "DirectShow video") {
			break
		}
		if inAudio && strings.Contains(line, "\"") {
			start := strings.Index(line, "\"")
			end := strings.LastIndex(line, "\"")
			if start >= 0 && end > start {
				devices = append(devices, line[start+1:end])
			}
		}
	}
	return devices
}
