//go:build linux

package voice

import (
	"reflect"
	"testing"
)

// Captured on Arch Linux with PipeWire (issue #23). ALSA's default is not
// routed to PipeWire here, so "default" is not listed.
const pipewireArecordL = `null
    Discard all samples (playback) or generate zero samples (capture)
lavrate
    Rate Converter Plugin Using Libav/FFmpeg Library
pipewire
    PipeWire Sound Server
pulse
    PulseAudio Sound Server
usbstream:CARD=NVidia
    HDA NVidia
    USB Stream Output
sysdefault:CARD=Generic_1
    HD-Audio Generic, ALC897 Analog
    Default Audio Device
front:CARD=Generic_1,DEV=0
    HD-Audio Generic, ALC897 Analog
    Front output / input
sysdefault:CARD=Audio
    AB13X USB Audio, USB Audio
    Default Audio Device
`

const pipewireArecordl = `**** List of CAPTURE Hardware Devices ****
card 2: Generic_1 [HD-Audio Generic], device 0: ALC897 Analog [ALC897 Analog]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
card 2: Generic_1 [HD-Audio Generic], device 2: ALC897 Alt Analog [ALC897 Alt Analog]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
card 3: Audio [AB13X USB Audio], device 0: USB Audio [USB Audio]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
`

const pipewirePactlSources = "60\talsa_output.usb-Generic_AB13X_USB_Audio_20210926172016-00.iec958-stereo.monitor\tPipeWire\ts16le 2ch 48000Hz\tSUSPENDED\n" +
	"61\talsa_input.usb-Generic_AB13X_USB_Audio_20210926172016-00.mono-fallback\tPipeWire\ts16le 1ch 48000Hz\tSUSPENDED\n" +
	"64\talsa_input.pci-0000_0b_00.6.analog-stereo\tPipeWire\ts32le 2ch 48000Hz\tSUSPENDED\n"

func TestParseALSAPCMNames(t *testing.T) {
	got := parseALSAPCMNames(pipewireArecordL)
	want := []string{
		"null", "lavrate", "pipewire", "pulse", "usbstream:CARD=NVidia",
		"sysdefault:CARD=Generic_1", "front:CARD=Generic_1,DEV=0", "sysdefault:CARD=Audio",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseALSAPCMNames = %q, want %q", got, want)
	}
}

func TestParseALSACaptureHardware(t *testing.T) {
	got := parseALSACaptureHardware(pipewireArecordl)
	want := []string{"plughw:CARD=Generic_1,DEV=0", "plughw:CARD=Generic_1,DEV=2", "plughw:CARD=Audio,DEV=0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseALSACaptureHardware = %q, want %q", got, want)
	}
}

func TestParsePulseSourcesSkipsMonitors(t *testing.T) {
	got := parsePulseSources(pipewirePactlSources)
	want := []string{
		"alsa_input.usb-Generic_AB13X_USB_Audio_20210926172016-00.mono-fallback",
		"alsa_input.pci-0000_0b_00.6.analog-stereo",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePulseSources = %q, want %q", got, want)
	}
}

func TestChooseDefaultCaptureDevice(t *testing.T) {
	running := func(servers ...string) func(string) bool {
		return func(server string) bool { return containsString(servers, server) }
	}
	tests := []struct {
		name    string
		pcms    []string
		running func(string) bool
		want    string
	}{
		{"unrouted default uses PipeWire", parseALSAPCMNames(pipewireArecordL), running("pipewire", "pulse"), "pipewire"},
		{"pipewire-pulse only", []string{"pulse"}, running("pulse"), "pulse"},
		{"configured default is kept", []string{"default", "pipewire"}, running("pipewire"), ""},
		{"plugin without server", []string{"pipewire", "pulse"}, running(), ""},
		{"plain ALSA", []string{"null", "sysdefault:CARD=PCH"}, running(), ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := chooseDefaultCaptureDevice(test.pcms, test.running); got != test.want {
				t.Fatalf("chooseDefaultCaptureDevice = %q, want %q", got, test.want)
			}
		})
	}
}
