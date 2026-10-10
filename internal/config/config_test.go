package config

import (
	"os"
	"path/filepath"
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

func TestEffectiveMachineID(t *testing.T) {
	setupTestConfig(t)
	gc := &GlobalConfig{Backend: "rclone", Remote: "r:p", MachineID: "deliberate"}
	if id := gc.EffectiveMachineID(); id != "deliberate" {
		t.Fatalf("expected 'deliberate', got %q", id)
	}

	// Unset: the hostname default EnsureMachineID would have persisted,
	// and — the point of the method — nothing written to disk.
	gc.MachineID = ""
	if id := gc.EffectiveMachineID(); id != DefaultMachineID() {
		t.Fatalf("expected the hostname default %q, got %q", DefaultMachineID(), id)
	}
	if _, err := os.Stat(paths.GlobalConfigTOML); err == nil {
		t.Fatal("EffectiveMachineID must not write config")
	}

	var nilConfig *GlobalConfig
	if id := nilConfig.EffectiveMachineID(); id != DefaultMachineID() {
		t.Fatalf("nil receiver: expected %q, got %q", DefaultMachineID(), id)
	}
}

// Fast listing is on unless the config says otherwise: a config.toml
// written before the key existed has no [sync] no_fast_list, and must
// still get the flag that takes a sync cycle from 148s to 10s.
func TestNoFastListDefaultsToOff(t *testing.T) {
	setupTestConfig(t)
	os.WriteFile(paths.GlobalConfigTOML,
		[]byte("[sync]\nbackend = \"rclone\"\nremote = \"hs:swarf-store\"\ndebounce = \"5s\"\n"), 0o644)
	if got := ReadGlobalConfig(); got.NoFastList {
		t.Fatal("a config without the key must leave fast listing enabled")
	}

	WriteGlobalConfig(&GlobalConfig{Backend: "rclone", Remote: "hs:swarf-store", Debounce: "5s", NoFastList: true})
	if got := ReadGlobalConfig(); !got.NoFastList {
		t.Fatal("no_fast_list = true must survive a write/read round trip")
	}
}

func TestExcludeRoundTrip(t *testing.T) {
	setupTestConfig(t)
	WriteGlobalConfig(&GlobalConfig{
		Backend: "rclone", Remote: "hs:swarf-store", Debounce: "5s",
		Exclude: []string{".yolo/", "*.tmp"},
	})
	got := ReadGlobalConfig()
	if len(got.Exclude) != 2 || got.Exclude[0] != ".yolo/" || got.Exclude[1] != "*.tmp" {
		t.Fatalf("exclude did not survive a write/read round trip: %v", got.Exclude)
	}

	os.WriteFile(paths.GlobalConfigTOML, []byte("[sync]\nbackend = \"git\"\nremote = \"x\"\ndebounce = \"5s\"\n"), 0o644)
	if got := ReadGlobalConfig(); len(got.Exclude) != 0 {
		t.Fatalf("expected no excludes by default, got %v", got.Exclude)
	}
}

// [sync] pull_interval is read from config and survives a rewrite (e.g.
// EnsureMachineID persisting a default machine id).
func TestPullIntervalRoundTrip(t *testing.T) {
	setupTestConfig(t)
	os.WriteFile(paths.GlobalConfigTOML, []byte("[sync]\nbackend = \"rclone\"\nremote = \"r:\"\npull_interval = \"10m\"\n"), 0o644)
	got := ReadGlobalConfig()
	if got == nil || got.PullInterval != "10m" {
		t.Fatalf("expected pull_interval=10m, got %+v", got)
	}
	EnsureMachineID()
	if got := ReadGlobalConfig(); got.PullInterval != "10m" {
		t.Fatalf("rewriting the config dropped pull_interval, got %q", got.PullInterval)
	}
}

// [sync] store_dir relocates the central store: the string survives a
// rewrite verbatim, and ReadGlobalConfig points paths.StoreDir at the
// resolved location.
func TestStoreDirRoundTrip(t *testing.T) {
	setupTestConfig(t)
	old := paths.StoreDir
	defer func() { paths.StoreDir = old }()

	dir := filepath.Join(t.TempDir(), "swarf-store")
	WriteGlobalConfig(&GlobalConfig{Backend: "git", Remote: "x", Debounce: "5s", StoreDir: dir})
	got := ReadGlobalConfig()
	if got.StoreDir != dir {
		t.Fatalf("store_dir did not survive a write/read round trip: %q", got.StoreDir)
	}
	if paths.StoreDir != dir {
		t.Fatalf("ReadGlobalConfig set StoreDir=%q, want %q", paths.StoreDir, dir)
	}
}

// A config with no store_dir must leave the default store location
// alone: every config written before the key existed lacks it.
func TestStoreDirDefaultUnchanged(t *testing.T) {
	setupTestConfig(t)
	old := paths.StoreDir
	defer func() { paths.StoreDir = old }()
	before := paths.StoreDir

	WriteGlobalConfig(&GlobalConfig{Backend: "git", Remote: "x", Debounce: "5s"})
	ReadGlobalConfig()
	if paths.StoreDir != before {
		t.Fatalf("a config without store_dir moved the store to %q", paths.StoreDir)
	}
}

// store_dir accepts ~ and relative paths: both resolve against the home
// directory, never the process working directory.
func TestResolveStoreDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	abs := filepath.Join(home, "abs-store")
	cases := []struct{ in, want string }{
		{abs, abs},
		{"~/rel-store", filepath.Join(home, "rel-store")},
		{"rel-store", filepath.Join(home, "rel-store")},
		{"  ~/padded  ", filepath.Join(home, "padded")},
	}
	for _, c := range cases {
		if got := resolveStoreDir(c.in); got != c.want {
			t.Errorf("resolveStoreDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParsePullInterval(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 0, true},
		{"0", 0, true},
		{" 0 ", 0, true},
		{"10m", 10 * time.Minute, true},
		{"1h", time.Hour, true},
		{"30s", 30 * time.Second, true},
		{"0s", 0, true},
		{"soon", 0, false},
		{"10", 0, false},
	}
	for _, c := range cases {
		got, err := ParsePullInterval(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("ParsePullInterval(%q) = %v, %v; want %v, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}
