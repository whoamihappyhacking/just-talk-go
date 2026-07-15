//go:build windows

package hotkey

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestWindowsProviderAppliesGlobalKeyStateEdges(t *testing.T) {
	provider, err := NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*windowsProvider)
	combo := Combo{Mods: ModAlt | ModSuper, Key: KeyNone}
	pressed := make(map[KeyCode]bool)
	p.keyDown = func(key KeyCode) bool { return pressed[key] }
	ch, err := p.Register(combo)
	if err != nil {
		t.Fatal(err)
	}

	pressed[KeyAlt] = true
	p.poll(time.Now())
	pressed[KeySuper] = true
	p.poll(time.Now())
	assertWindowsEvent(t, ch, combo, KeyDown)

	// An unchanged polling sample must not produce key repeat events.
	p.poll(time.Now())
	select {
	case event := <-ch:
		t.Fatalf("unchanged key state was dispatched: %+v", event)
	default:
	}

	delete(pressed, KeySuper)
	p.poll(time.Now())
	assertWindowsEvent(t, ch, combo, KeyUp)
}

func TestWindowsProviderUsesHookStateWhenPollingMissesSuper(t *testing.T) {
	provider, err := NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*windowsProvider)
	p.pollKeyDown = func(KeyCode) bool { return false }

	p.setHookVirtualKey(vkLWin, true)
	if !p.combinedKeyDown(KeySuper) {
		t.Fatal("left Windows key hook state did not provide Super")
	}
	p.setHookVirtualKey(vkRWin, true)
	p.setHookVirtualKey(vkLWin, false)
	if !p.combinedKeyDown(KeySuper) {
		t.Fatal("releasing left Windows key cleared the held right Windows key")
	}
	p.setHookVirtualKey(vkRWin, false)
	if p.combinedKeyDown(KeySuper) {
		t.Fatal("Super remained active after both Windows keys were released")
	}
}

func TestWindowsHookFallbackIntegration(t *testing.T) {
	if os.Getenv("JUST_TALK_TEST_WINDOWS_HOTKEY") == "" {
		t.Skip("set JUST_TALK_TEST_WINDOWS_HOTKEY=1 to test the low-level keyboard hook")
	}
	provider, err := NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	p := provider.(*windowsProvider)
	p.pollKeyDown = func(KeyCode) bool { return false }
	combo := Combo{Mods: ModAlt | ModSuper, Key: KeyNone}
	ch, err := p.Register(combo)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	go func() { startDone <- p.Start(ctx) }()
	time.Sleep(250 * time.Millisecond)

	keybdEvent := modUser32.NewProc("keybd_event")
	keybdEvent.Call(vkLMenu, 0, 0, 0)
	time.Sleep(30 * time.Millisecond)
	keybdEvent.Call(vkLWin, 0, 0, 0)
	assertWindowsEventWithin(t, ch, combo, KeyDown, 2*time.Second)
	keybdEvent.Call(vkLWin, 0, 2, 0)
	keybdEvent.Call(vkLMenu, 0, 2, 0)
	assertWindowsEventWithin(t, ch, combo, KeyUp, 2*time.Second)
	keybdEvent.Call(vkEscape, 0, 0, 0)
	keybdEvent.Call(vkEscape, 0, 2, 0)

	cancel()
	select {
	case err := <-startDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("provider Start error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not stop after cancellation")
	}
}

func assertWindowsEvent(t *testing.T, ch <-chan Event, combo Combo, eventType EventType) {
	t.Helper()
	select {
	case event := <-ch:
		if event.Combo != combo || event.Type != eventType {
			t.Fatalf("unexpected event: %+v", event)
		}
	default:
		t.Fatalf("expected %s for %s", eventType, combo)
	}
}

func assertWindowsEventWithin(t *testing.T, ch <-chan Event, combo Combo, eventType EventType, timeout time.Duration) {
	t.Helper()
	select {
	case event := <-ch:
		if event.Combo != combo || event.Type != eventType {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %s for %s", eventType, combo)
	}
}
