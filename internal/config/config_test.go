package config

import (
	"os"
	"path/filepath"
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
