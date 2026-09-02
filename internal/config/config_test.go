package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if c, err := Load(p); err != nil || c.Theme != "" {
		t.Errorf("missing config = %+v, %v", c, err)
	}
	os.WriteFile(p, []byte(`{"theme":"mono","ascii":true,"sort":"due","keys":{"quit":["x"]}}`), 0o644)
	c, err := Load(p)
	if err != nil || c.Theme != "mono" || !c.ASCII || c.Sort != "due" || c.Keys["quit"][0] != "x" {
		t.Errorf("config = %+v, %v", c, err)
	}
	os.WriteFile(p, []byte(`{"keys":{"fly":["f"]}}`), 0o644)
	if _, err := Load(p); err == nil {
		t.Error("unknown key action should fail")
	}
}

func TestLoadHonoursRenamedKeys(t *testing.T) {
	renamedKeys["compact_cards"] = "compact"
	defer delete(renamedKeys, "compact_cards")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"compact_cards": true, "theme": "mono"}`), 0o644) //nolint:errcheck // test data

	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Compact || c.Theme != "mono" {
		t.Fatalf("old key not honoured: %+v", c)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], `"compact_cards"`) || !strings.Contains(c.Warnings[0], `"compact"`) {
		t.Fatalf("warnings = %v", c.Warnings)
	}

	// The new key wins when both are present, and there is still a warning
	// about the stale one.
	os.WriteFile(path, []byte(`{"compact_cards": true, "compact": false}`), 0o644) //nolint:errcheck // test data
	c, err = Load(path)
	if err != nil || c.Compact || len(c.Warnings) != 1 {
		t.Fatalf("both keys: %+v err=%v", c, err)
	}

	// Unknown keys are still ignored silently (forward compatibility).
	os.WriteFile(path, []byte(`{"from_the_future": 1}`), 0o644) //nolint:errcheck // test data
	c, err = Load(path)
	if err != nil || len(c.Warnings) != 0 {
		t.Fatalf("unknown key: %+v err=%v", c, err)
	}
}
