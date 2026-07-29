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
	p := newWindowsTestProvider(t)
	combo := Combo{Mods: ModAlt | ModSuper, Key: KeyNone}
	pressed := make(map[KeyCode]bool)
	p.pollKeyDown = func(key KeyCode) bool { return pressed[key] }
	ch, err := p.Register(combo)
	if err != nil {
		t.Fatal(err)
	}

	pressed[KeyAlt] = true
	p.poll(time.Now())
	pressed[KeySuper] = true
	p.poll(time.Now())
	assertWindowsEvent(t, ch, combo, KeyDown)

	p.poll(time.Now())
	assertNoWindowsEvent(t, ch)

	delete(pressed, KeySuper)
	p.poll(time.Now())
	assertWindowsEvent(t, ch, combo, KeyUp)
}

func TestWindowsModifierCombosRequireExactModifiers(t *testing.T) {
	modifierOnly := Combo{Mods: ModAlt | ModSuper, Key: KeyNone}
	if windowsComboActiveWithModifiers(modifierOnly, ModAlt|ModShift|ModSuper, nil) {
		t.Fatal("modifier-only combo accepted an extra Shift modifier")
	}
	standard := Combo{Mods: ModAlt, Key: KeyF9}
	if windowsComboActiveWithModifiers(standard, ModAlt|ModShift, func(key KeyCode) bool { return key == KeyF9 }) {
		t.Fatal("standard combo accepted an extra Shift modifier")
	}
}

func TestWindowsHookTracksSystemShortcutsWithoutStuckState(t *testing.T) {
	p := newWindowsTestProvider(t)
	combo := Combo{Mods: ModAlt | ModSuper, Key: KeyNone}
	if _, err := p.RegisterWithOptions(combo, RegisterOptions{Suppress: true}); err != nil {
		t.Fatal(err)
	}

	events := []windowsLowLevelKeyEvent{
		{VirtualKey: vkLMenu},
		{VirtualKey: vkTab},
		{VirtualKey: vkTab, Flags: llkhfUp},
		{VirtualKey: vkLMenu, Flags: llkhfUp},
		{VirtualKey: vkLWin},
		{VirtualKey: vkLWin, Flags: llkhfUp},
	}
	for _, event := range events {
		p.recordHookEvent(event)
	}
	if p.hookKeyDown(KeyAlt) || p.hookKeyDown(KeySuper) || p.hookKeyDown(KeyTab) {
		t.Fatal("hook retained state after Alt, Alt+Tab, and Super key-up events")
	}
}

func TestWindowsClearsStaleModifierBeforeOtherModifierPress(t *testing.T) {
	tests := []struct {
		name          string
		staleKey      KeyCode
		staleVK       uint32
		standaloneKey KeyCode
		standaloneVK  uint32
	}{
		{
			name:          "Alt_then_Super",
			staleKey:      KeyAlt,
			staleVK:       vkLMenu,
			standaloneKey: KeySuper,
			standaloneVK:  vkLWin,
		},
		{
			name:          "Super_then_Alt",
			staleKey:      KeySuper,
			staleVK:       vkLWin,
			standaloneKey: KeyAlt,
			standaloneVK:  vkLMenu,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := newWindowsTestProvider(t)
			combo := Combo{Mods: ModAlt | ModSuper, Key: KeyNone}
			pressed := make(map[KeyCode]bool)
			p.pollKeyDown = func(key KeyCode) bool { return pressed[key] }
			ch, err := p.RegisterWithOptions(combo, RegisterOptions{Suppress: true})
			if err != nil {
				t.Fatal(err)
			}

			pressed[test.staleKey] = true
			p.recordHookEvent(windowsLowLevelKeyEvent{VirtualKey: test.staleVK})
			p.poll(time.Now())
			assertNoWindowsEvent(t, ch)

			// The physical key was released, but its hook key-up was lost.
			delete(pressed, test.staleKey)
			pressed[test.standaloneKey] = true
			p.recordHookEvent(windowsLowLevelKeyEvent{VirtualKey: test.standaloneVK})
			if p.hookKeyDown(test.staleKey) {
				t.Fatalf("stale %s hook state remained after %s was pressed", test.staleKey, test.standaloneKey)
			}
			p.poll(time.Now())
			assertNoWindowsEvent(t, ch)
		})
	}
}

func TestWindowsClearsStaleModifierAfterPhysicalRelease(t *testing.T) {
	p := newWindowsTestProvider(t)
	combo := Combo{Mods: ModAlt | ModSuper, Key: KeyNone}
	pressed := make(map[KeyCode]bool)
	p.pollKeyDown = func(key KeyCode) bool { return pressed[key] }
	ch, err := p.Register(combo)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	pressed[KeyAlt] = true
	p.recordHookEvent(windowsLowLevelKeyEvent{VirtualKey: vkLMenu})
	pressed[KeySuper] = true
	p.recordHookEvent(windowsLowLevelKeyEvent{VirtualKey: vkLWin})
	p.poll(now)
	assertWindowsEvent(t, ch, combo, KeyDown)

	delete(pressed, KeyAlt)
	p.recordHookEvent(windowsLowLevelKeyEvent{VirtualKey: vkLMenu, Flags: llkhfUp})
	delete(pressed, KeySuper) // Simulate a lost Super hook key-up.
	p.poll(now.Add(windowsPollInterval))
	assertWindowsEvent(t, ch, combo, KeyUp)
	p.poll(now.Add(windowsHookResetLag + 2*windowsPollInterval))
	if p.hookKeyDown(KeySuper) {
		t.Fatal("stale Super hook state remained after the physical release")
	}
}

func TestWindowsProviderUsesHookStateWhenPollingMissesSuper(t *testing.T) {
	p := newWindowsTestProvider(t)
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

func TestWindowsProviderDoesNotUseHookFallbackForRegularKeys(t *testing.T) {
	p := newWindowsTestProvider(t)
	p.pollKeyDown = func(KeyCode) bool { return false }
	p.setHookVirtualKey(vkF9, true)
	if p.combinedKeyDown(KeyF9) {
		t.Fatal("regular key used stale low-level hook state as an active key")
	}
}

func TestWindowsHookIntegration(t *testing.T) {
	if os.Getenv("JUST_TALK_TEST_WINDOWS_HOTKEY") == "" {
		t.Skip("set JUST_TALK_TEST_WINDOWS_HOTKEY=1 to test the low-level keyboard hook")
	}
	p := newWindowsTestProvider(t)
	combo := Combo{Mods: ModAlt | ModSuper, Key: KeyNone}
	ch, err := p.RegisterWithOptions(combo, RegisterOptions{Suppress: true})
	if err != nil {
		t.Fatal(err)
	}

	keybdEvent := modUser32.NewProc("keybd_event")
	ctx, cancel := context.WithCancel(context.Background())
	startDone := make(chan error, 1)
	stopped := false
	defer func() {
		keybdEvent.Call(vkLWin, 0, 2, 0)
		keybdEvent.Call(vkLMenu, 0, 2, 0)
		keybdEvent.Call(vkEscape, 0, 0, 0)
		keybdEvent.Call(vkEscape, 0, 2, 0)
		if stopped {
			return
		}
		cancel()
		select {
		case <-startDone:
		case <-time.After(2 * time.Second):
		}
	}()
	go func() { startDone <- p.Start(ctx) }()
	time.Sleep(250 * time.Millisecond)

	for cycle := 0; cycle < 3; cycle++ {
		keybdEvent.Call(vkLMenu, 0, 0, 0)
		time.Sleep(30 * time.Millisecond)
		keybdEvent.Call(vkLWin, 0, 0, 0)
		assertWindowsEventWithin(t, ch, combo, KeyDown, 2*time.Second)
		keybdEvent.Call(vkLWin, 0, 2, 0)
		keybdEvent.Call(vkLMenu, 0, 2, 0)
		assertWindowsEventWithin(t, ch, combo, KeyUp, 2*time.Second)

		keybdEvent.Call(vkLMenu, 0, 0, 0)
		keybdEvent.Call(vkLMenu, 0, 2, 0)
		assertNoWindowsEventWithin(t, ch, 100*time.Millisecond)
		keybdEvent.Call(vkLWin, 0, 0, 0)
		keybdEvent.Call(vkLWin, 0, 2, 0)
		assertNoWindowsEventWithin(t, ch, 100*time.Millisecond)
	}

	cancel()
	select {
	case err := <-startDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("provider Start error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not stop after cancellation")
	}
	stopped = true
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

func assertNoWindowsEvent(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case event := <-ch:
		t.Fatalf("unexpected Windows hotkey event: %+v", event)
	default:
	}
}

func assertNoWindowsEventWithin(t *testing.T, ch <-chan Event, timeout time.Duration) {
	t.Helper()
	select {
	case event := <-ch:
		t.Fatalf("unexpected Windows hotkey event: %+v", event)
	case <-time.After(timeout):
	}
}

func newWindowsTestProvider(t *testing.T) *windowsProvider {
	t.Helper()
	provider, err := NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	return provider.(*windowsProvider)
}
