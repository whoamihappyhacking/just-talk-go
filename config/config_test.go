package config

import (
	"os"
	"path/filepath"
	"testing"
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
