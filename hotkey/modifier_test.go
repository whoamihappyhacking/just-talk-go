package hotkey

import (
	"testing"
	"time"
)

func TestModifierSatisfiesSides(t *testing.T) {
	tests := []struct {
		name     string
		held     Modifier
		required Modifier
		want     bool
	}{
		{"family accepts left", ModLeftAlt, ModAlt, true},
		{"family accepts right", ModRightAlt, ModAlt, true},
		{"family accepts unknown side", ModAlt, ModAlt, true},
		{"right accepts right", ModRightAlt, ModRightAlt, true},
		{"right rejects left", ModLeftAlt, ModRightAlt, false},
		{"right rejects unknown side", ModAlt, ModRightAlt, false},
		{"right accepts both sides", ModLeftAlt | ModRightAlt, ModRightAlt, true},
		{"extra family rejected", ModRightAlt | ModLeftCtrl, ModRightAlt, false},
		{"sided combo", ModRightCtrl | ModLeftShift, ModRightCtrl | ModShift, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.held.Satisfies(test.required); got != test.want {
				t.Fatalf("%s.Satisfies(%s) = %v, want %v", test.held, test.required, got, test.want)
			}
		})
	}
}

func TestModifierString(t *testing.T) {
	tests := map[Modifier]string{
		ModAlt | ModSuper:            "Alt+Super",
		ModRightAlt:                  "RightAlt",
		ModLeftCtrl | ModRightAlt:    "LeftCtrl+RightAlt",
		ModLeftShift | ModRightShift: "LeftShift+RightShift",
	}
	for mods, want := range tests {
		if got := mods.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}

func TestReleaseEndsComboOnlyForRequiredSide(t *testing.T) {
	right := Combo{Mods: ModRightAlt}
	if releaseEndsCombo(right, KeyLeftAlt) {
		t.Fatal("releasing left Alt ended a RightAlt combo")
	}
	if !releaseEndsCombo(right, KeyRightAlt) {
		t.Fatal("releasing right Alt did not end a RightAlt combo")
	}
	either := Combo{Mods: ModAlt, Key: KeyF9}
	if !releaseEndsCombo(either, KeyLeftAlt) || !releaseEndsCombo(either, KeyF9) {
		t.Fatal("releasing a member did not end an Alt+F9 combo")
	}
	if releaseEndsCombo(either, KeyRightCtrl) {
		t.Fatal("releasing an unrelated modifier ended Alt+F9")
	}
}

func TestTrackerRightModifierOnlyIgnoresLeftKey(t *testing.T) {
	tracker := NewKeyStateTracker()
	combo := Combo{Mods: ModRightAlt}
	tracker.Watch(combo, make(chan Event, 4))

	if events := tracker.KeyDown(KeyLeftAlt, time.Now()); len(events) != 0 {
		t.Fatalf("left Alt down emitted %#v, want nothing", events)
	}
	if events := tracker.KeyUp(KeyLeftAlt, time.Now()); len(events) != 0 {
		t.Fatalf("left Alt up emitted %#v, want nothing", events)
	}

	events := tracker.KeyDown(KeyRightAlt, time.Now())
	if len(events) != 1 || events[0].Combo != combo || events[0].Type != KeyDown {
		t.Fatalf("right Alt down emitted %#v, want one KeyDown", events)
	}
	events = tracker.KeyUp(KeyRightAlt, time.Now())
	if len(events) != 1 || events[0].Combo != combo || events[0].Type != KeyUp {
		t.Fatalf("right Alt up emitted %#v, want one KeyUp", events)
	}
}

func TestTrackerFamilyModifierStaysHeldWhileOtherSideIsDown(t *testing.T) {
	tracker := NewKeyStateTracker()
	combo := Combo{Mods: ModAlt}
	tracker.Watch(combo, make(chan Event, 4))

	events := tracker.KeyDown(KeyLeftAlt, time.Now())
	if len(events) != 1 || events[0].Type != KeyDown {
		t.Fatalf("left Alt down emitted %#v, want one KeyDown", events)
	}
	if events := tracker.KeyDown(KeyRightAlt, time.Now()); len(events) != 0 {
		t.Fatalf("right Alt down emitted %#v, want nothing", events)
	}
	if events := tracker.KeyUp(KeyLeftAlt, time.Now()); len(events) != 0 {
		t.Fatalf("left Alt up emitted %#v while right Alt is held, want nothing", events)
	}
	if tracker.ActiveMods() != ModRightAlt {
		t.Fatalf("active mods = %s, want RightAlt", tracker.ActiveMods())
	}
	events = tracker.KeyUp(KeyRightAlt, time.Now())
	if len(events) != 1 || events[0].Type != KeyUp {
		t.Fatalf("right Alt up emitted %#v, want one KeyUp", events)
	}
}

func TestTrackerSidedStandardCombo(t *testing.T) {
	tracker := NewKeyStateTracker()
	combo := Combo{Mods: ModRightCtrl, Key: KeyF9}
	tracker.Watch(combo, make(chan Event, 4))

	tracker.KeyDown(KeyLeftCtrl, time.Now())
	if events := tracker.KeyDown(KeyF9, time.Now()); len(events) != 0 {
		t.Fatalf("LeftCtrl+F9 emitted %#v for RightCtrl+F9", events)
	}
	tracker.KeyUp(KeyF9, time.Now())
	tracker.KeyUp(KeyLeftCtrl, time.Now())

	tracker.KeyDown(KeyRightCtrl, time.Now())
	events := tracker.KeyDown(KeyF9, time.Now())
	if len(events) != 1 || events[0].Combo != combo || events[0].Type != KeyDown {
		t.Fatalf("RightCtrl+F9 emitted %#v, want one KeyDown", events)
	}
	events = tracker.KeyUp(KeyRightCtrl, time.Now())
	if len(events) != 1 || events[0].Type != KeyUp {
		t.Fatalf("RightCtrl up emitted %#v, want one KeyUp", events)
	}
}
