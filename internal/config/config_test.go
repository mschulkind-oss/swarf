package config

import (
	"os"
	"testing"
	"time"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

func setupTestConfig(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	paths.ConfigDir = tmp
	paths.GlobalConfigTOML = tmp + "/config.toml"
	paths.DrawersTOML = tmp + "/drawers.toml"
	return tmp
}

func TestReadGlobalConfigMissing(t *testing.T) {
	setupTestConfig(t)
	if ReadGlobalConfig() != nil {
		t.Fatal("expected nil for missing config")
	}
}

func TestWriteReadGlobalConfig(t *testing.T) {
	setupTestConfig(t)
	c := &GlobalConfig{Backend: "git", Remote: "git@example.com:repo.git", Debounce: "10s"}
	if err := WriteGlobalConfig(c); err != nil {
		t.Fatal(err)
	}
	got := ReadGlobalConfig()
	if got == nil {
		t.Fatal("expected config, got nil")
	}
	if got.Backend != "git" {
		t.Fatalf("expected backend=git, got %s", got.Backend)
	}
	if got.Remote != "git@example.com:repo.git" {
		t.Fatalf("expected remote, got %s", got.Remote)
	}
}

func TestDrawerRegistry(t *testing.T) {
	setupTestConfig(t)

	drawers := ReadDrawers()
	if len(drawers) != 0 {
		t.Fatal("expected empty drawers")
	}

	RegisterDrawer("myproject", "/tmp/myproject")
	drawers = ReadDrawers()
	if len(drawers) != 1 || drawers[0].Slug != "myproject" {
		t.Fatalf("expected 1 drawer with slug=myproject, got %v", drawers)
	}

	// Idempotent
	RegisterDrawer("myproject", "/tmp/myproject")
	drawers = ReadDrawers()
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}

	// Multiple
	RegisterDrawer("other", "/tmp/other")
	drawers = ReadDrawers()
	if len(drawers) != 2 {
		t.Fatalf("expected 2 drawers, got %d", len(drawers))
	}

	// Unregister
	UnregisterDrawer("myproject")
	drawers = ReadDrawers()
	if len(drawers) != 1 || drawers[0].Slug != "other" {
		t.Fatalf("expected 1 drawer with slug=other, got %v", drawers)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"5s", 5 * time.Second},
		{"500ms", 500 * time.Millisecond},
		{"2m", 2 * time.Minute},
		{"1h", 1 * time.Hour},
		{"  5s  ", 5 * time.Second},
		{"1.5s", 1500 * time.Millisecond},
	}
	for _, tt := range tests {
		got, err := ParseDuration(tt.input)
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}

	// Invalid
	for _, bad := range []string{"", "abc", "5x", "5"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestDefaultMachineID(t *testing.T) {
	id := DefaultMachineID()
	if id == "" {
		t.Fatal("expected non-empty default machine id")
	}
	// Must only contain [a-z0-9_-].
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			t.Fatalf("unexpected char %q in machine id %q", r, id)
		}
	}
}

func TestEnsureMachineID_WritesDefault(t *testing.T) {
	setupTestConfig(t)
	// Writing config without an explicit MachineID should leave the field empty.
	WriteGlobalConfig(&GlobalConfig{Backend: "rclone", Remote: "r:p", Debounce: "5s"})
	gc := ReadGlobalConfig()
	if gc.MachineID != "" {
		t.Fatalf("expected empty MachineID, got %q", gc.MachineID)
	}
	// EnsureMachineID writes the hostname default and returns it.
	id := EnsureMachineID()
	if id == "" {
		t.Fatal("expected non-empty id from EnsureMachineID")
	}
	gc = ReadGlobalConfig()
	if gc.MachineID != id {
		t.Fatalf("expected persisted id %q, got %q", id, gc.MachineID)
	}
	// Calling again is stable.
	if EnsureMachineID() != id {
		t.Fatal("EnsureMachineID must be stable across calls")
	}
}

func TestEnsureMachineID_RespectsExplicit(t *testing.T) {
	setupTestConfig(t)
	WriteGlobalConfig(&GlobalConfig{Backend: "rclone", Remote: "r:p", Debounce: "5s", MachineID: "deliberate"})
	if id := EnsureMachineID(); id != "deliberate" {
		t.Fatalf("expected 'deliberate', got %q", id)
	}
}

func TestAutoSweepConfig(t *testing.T) {
	setupTestConfig(t)
	c := &GlobalConfig{
		Backend:   "git",
		Remote:    "test",
		Debounce:  "5s",
		AutoSweep: []string{"AGENTS.md", ".cursorrules"},
	}
	WriteGlobalConfig(c)
	got := ReadGlobalConfig()
	if len(got.AutoSweep) != 2 {
		t.Fatalf("expected 2 auto_sweep paths, got %v", got.AutoSweep)
	}

	// Empty file check
	os.WriteFile(paths.GlobalConfigTOML, []byte("[sync]\nbackend = \"git\"\nremote = \"x\"\ndebounce = \"5s\"\n"), 0o644)
	got = ReadGlobalConfig()
	if len(got.AutoSweep) != 0 {
		t.Fatalf("expected empty auto_sweep, got %v", got.AutoSweep)
	}
}
