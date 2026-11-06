package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c/just-talk-go/hotkey"
)

func TestSaveTrafficDefaultsAndRoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())
	if Default().Voice.SaveTraffic {
		t.Fatal("file recognition must be opt-in")
	}
	path := filepath.Join(".", "config.toml")
	if err := os.WriteFile(path, []byte("[voice]\nmode = \"hold\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Voice.SaveTraffic || cfg.Voice.Mode != "hold" {
		t.Fatal("legacy config changed behavior")
	}
	for _, on := range []bool{true, false} {
		cfg.Voice.SaveTraffic = on
		if err := Save(cfg); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Voice.SaveTraffic != on || got.Voice.ResourceID != "volc.bigasr.sauc.duration" {
			t.Fatalf("roundtrip changed mode/resource: %+v", got.Voice)
		}
	}
}

func TestParseHotkeySidedModifiers(t *testing.T) {
	tests := []struct {
		in   string
		want hotkey.Combo
		name string
	}{
		{"RightOption", hotkey.Combo{Mods: hotkey.ModRightAlt}, "RightAlt"},
		{"rightcmd", hotkey.Combo{Mods: hotkey.ModRightSuper}, "RightSuper"},
		{"RightCtrl", hotkey.Combo{Mods: hotkey.ModRightCtrl}, "RightCtrl"},
		{"RightShift", hotkey.Combo{Mods: hotkey.ModRightShift}, "RightShift"},
		{"LeftAlt+Super", hotkey.Combo{Mods: hotkey.ModLeftAlt | hotkey.ModSuper}, "LeftAlt+Super"},
		{"RightControl+F9", hotkey.Combo{Mods: hotkey.ModRightCtrl, Key: hotkey.KeyF9}, "RightCtrl+F9"},
		{"Option", hotkey.Combo{Mods: hotkey.ModAlt}, "Alt"},
	}
	for _, test := range tests {
		got, err := ParseHotkey(test.in)
		if err != nil {
			t.Fatalf("ParseHotkey(%q): %v", test.in, err)
		}
		if got != test.want {
			t.Fatalf("ParseHotkey(%q) = %#v, want %#v", test.in, got, test.want)
		}
		if got.String() != test.name {
			t.Fatalf("ParseHotkey(%q).String() = %q, want %q", test.in, got.String(), test.name)
		}
		again, err := ParseHotkey(got.String())
		if err != nil || again != got {
			t.Fatalf("ParseHotkey(%q) round trip = %#v, %v", got.String(), again, err)
		}
	}
}
