//go:build darwin

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
	sox := []string{"-r", "16000", "-b", "16", "-c", "1", "-t", "raw"}
	ffmpeg := []string{"-ar", "16000", "-ac", "1", "-f", "s16le", "-loglevel", "quiet"}
	rec := []string{"-r", "16000", "-b", "16", "-c", "1", "-t", "raw"}

	if device != "" {
		sox = append([]string{"-t", "coreaudio", device}, sox...)
		ffmpeg = append([]string{"-f", "avfoundation", "-i", device}, ffmpeg...)
		rec = append([]string{"-t", "coreaudio", device}, rec...)
	} else {
		sox = append([]string{"-t", "coreaudio", "default"}, sox...)
		ffmpeg = append([]string{"-f", "avfoundation", "-i", ":0"}, ffmpeg...)
		rec = append([]string{"default"}, rec...)
	}

	sox = append(sox, "-")
	ffmpeg = append(ffmpeg, "-")
	rec = append(rec, "-")

	return firstFound([]candidate{
		{"sox", sox},
		{"ffmpeg", ffmpeg},
		{"rec", rec},
	})
}

func errNoBackend(candidates []candidate) error {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.name
	}
	return fmt.Errorf("no recording backend found; install one of: %s", strings.Join(names, ", "))
}

// ListDevices returns available audio input devices on macOS.
func ListDevices() ([]string, error) {
	// ffmpeg can list AVFoundation devices
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		cmd := exec.Command(ff, "-f", "avfoundation", "-list_devices", "true", "-i", "\"\"")
		out, _ := cmd.CombinedOutput()
		return parseAVFoundationDevices(string(out)), nil
	}
	// sox can list coreaudio devices
	if sox, err := exec.LookPath("sox"); err == nil {
		cmd := exec.Command(sox, "-V", "-t", "coreaudio", "default", "-n", "stat")
		out, _ := cmd.CombinedOutput()
		_ = out
	}
	return nil, fmt.Errorf("install ffmpeg or sox for device listing")
}

func parseAVFoundationDevices(output string) []string {
	var devices []string
	lines := strings.Split(output, "\n")
	inAudio := false
	for _, line := range lines {
		if strings.Contains(line, "AVFoundation audio") {
			inAudio = true
			continue
		}
		if inAudio && strings.Contains(line, "AVFoundation video") {
			break
		}
		if inAudio {
			// Format: [0] FaceTime HD Camera
			if idx := strings.Index(line, "] "); idx > 0 {
				devices = append(devices, strings.TrimSpace(line[idx+2:]))
			}
		}
	}
	return devices
}
