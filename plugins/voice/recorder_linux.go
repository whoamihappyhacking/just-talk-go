//go:build linux

package voice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var errArecordMissing = errors.New("arecord not found; install alsa-utils")

var errNoInputDevices = errors.New("no audio input devices found; check that a microphone is connected, " +
	"that your user can open /dev/snd (audio group), and that PipeWire or PulseAudio is running")

// Listing devices only reads ALSA configuration, but a wedged sound server
// must not hold up recording start.
const deviceListTimeout = 2 * time.Second

func init() {
	// Resolve the default capture device off the hotkey path.
	prepareCapture = func() { go cachedALSAPCMNames() }
}

func pickCommand() (*exec.Cmd, string, error) {
	return pickCommandWithDevice("")
}

func pickCommandWithDevice(device string) (*exec.Cmd, string, error) {
	if device == "" {
		device = defaultCaptureDevice()
	}
	// ALSA arecord: reliable s16le output, proper format conversion
	al := []string{"-r", "16000", "-f", "S16_LE", "-c", "1", "-t", "raw"}
	if device != "" {
		al = append(al, "-D", device)
	}
	al = append(al, "-")

	cmd, name, err := firstFound([]candidate{
		{"arecord", al},
	})
	if err == nil && device != "" {
		name += " (" + device + ")"
	}
	return cmd, name, err
}

func errNoBackend(candidates []candidate) error {
	return errArecordMissing
}

// defaultCaptureDevice returns the arecord device used when none is
// configured, or "" for ALSA's default.
func defaultCaptureDevice() string {
	return chooseDefaultCaptureDevice(cachedALSAPCMNames(), soundServerRunning)
}

// chooseDefaultCaptureDevice keeps ALSA's default when it is listed, which
// means pipewire-alsa, pulseaudio-alsa, or the user configured it. Otherwise
// ALSA's built-in default opens the first sound card directly: that fails
// while a sound server holds the hardware, or picks a card without a
// microphone. Record through the running sound server instead.
func chooseDefaultCaptureDevice(pcms []string, running func(server string) bool) string {
	if containsString(pcms, "default") {
		return ""
	}
	for _, server := range []string{"pipewire", "pulse"} {
		if containsString(pcms, server) && running(server) {
			return server
		}
	}
	return ""
}

func soundServerRunning(server string) bool {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	switch server {
	case "pipewire":
		return isSocket(filepath.Join(dir, "pipewire-0"))
	case "pulse":
		return os.Getenv("PULSE_SERVER") != "" || isSocket(filepath.Join(dir, "pulse", "native"))
	}
	return false
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

var alsaPCMCache struct {
	once  sync.Once
	names []string
}

// cachedALSAPCMNames lists ALSA PCMs once per process; they come from
// configuration files that do not change while Just Talk runs.
func cachedALSAPCMNames() []string {
	alsaPCMCache.once.Do(func() {
		alsaPCMCache.names, _ = listALSAPCMNames()
	})
	return alsaPCMCache.names
}

func listALSAPCMNames() ([]string, error) {
	out, err := runListCommand("arecord", "-L")
	if err != nil {
		return nil, err
	}
	return parseALSAPCMNames(out), nil
}

// ListDevices returns capture devices usable as the voice.device setting,
// sound-server devices first because on desktops the server owns the
// hardware.
func ListDevices() ([]string, error) {
	if _, err := exec.LookPath("arecord"); err != nil {
		return nil, errArecordMissing
	}

	var devices []string
	add := func(name string) {
		if name != "" && !containsString(devices, name) {
			devices = append(devices, name)
		}
	}

	pcms, pcmErr := listALSAPCMNames()
	for _, name := range []string{"default", "pipewire", "pulse"} {
		if containsString(pcms, name) {
			add(name)
		}
	}
	if containsString(pcms, "pipewire") {
		if out, err := runListCommand("pactl", "list", "sources", "short"); err == nil {
			for _, source := range parsePulseSources(out) {
				add("pipewire:NODE=" + source)
			}
		}
	}

	out, hwErr := runListCommand("arecord", "-l")
	if hwErr == nil {
		for _, device := range parseALSACaptureHardware(out) {
			add(device)
		}
	}

	if len(devices) == 0 {
		if pcmErr != nil && hwErr != nil {
			return nil, fmt.Errorf("list audio input devices: %w", errors.Join(pcmErr, hwErr))
		}
		return nil, errNoInputDevices
	}
	return devices, nil
}

func runListCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deviceListTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// parseALSAPCMNames extracts PCM names from `arecord -L`. Names start in
// column 0; their descriptions follow on indented lines.
func parseALSAPCMNames(output string) []string {
	var names []string
	for _, line := range strings.Split(output, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		names = append(names, strings.TrimSpace(line))
	}
	return names
}

var alsaCaptureLine = regexp.MustCompile(`^card \d+: (\S+) \[.*\], device (\d+):`)

// parseALSACaptureHardware turns `arecord -l` capture devices into plughw
// names. Card IDs stay stable across reboots, unlike card indexes.
func parseALSACaptureHardware(output string) []string {
	var devices []string
	for _, line := range strings.Split(output, "\n") {
		if m := alsaCaptureLine.FindStringSubmatch(line); m != nil {
			devices = append(devices, "plughw:CARD="+m[1]+",DEV="+m[2])
		}
	}
	return devices
}

var safeNodeName = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// parsePulseSources returns input source names from
// `pactl list sources short`, skipping monitors of output devices.
// PipeWire node names match these source names.
func parsePulseSources(output string) []string {
	var sources []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		name := fields[1]
		// Names outside this set would need ALSA argument quoting.
		if strings.HasSuffix(name, ".monitor") || !safeNodeName.MatchString(name) {
			continue
		}
		sources = append(sources, name)
	}
	return sources
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
