package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/c/just-talk-go/config"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSaveTrafficTogglePersistsFromKeyboard(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("config.toml", []byte("[voice]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(config.Default())
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	index := -1
	for i, f := range m.fields {
		if f.key == "save_traffic" {
			index = i
		}
	}
	if index == -1 {
		t.Fatal("missing 省流量 toggle")
	}
	m.cursor = index
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	cfg, err := config.Load("config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Voice.SaveTraffic || !strings.Contains(m.View(), "省流量") {
		t.Fatal("toggle did not save/render")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	cfg, err = config.Load("config.toml")
	if err != nil || cfg.Voice.SaveTraffic {
		t.Fatal("could not switch back to WebSocket")
	}
}
