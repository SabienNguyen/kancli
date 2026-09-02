package ui

import (
	"testing"
)

func TestThemes(t *testing.T) {
	for _, name := range ThemeNames {
		if _, err := ThemeByName(name, true); err != nil {
			t.Errorf("theme %s: %v", name, err)
		}
	}
	if _, err := ThemeByName("neon", false); err == nil {
		t.Error("unknown theme should fail")
	}
	th, _ := ThemeByName("mono", true)
	if !th.mono || th.border.TopLeft != "+" {
		t.Error("mono/ascii theme misconfigured")
	}
}
