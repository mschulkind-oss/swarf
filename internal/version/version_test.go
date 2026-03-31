package version

import "testing"

func TestStringDev(t *testing.T) {
	Version = "dev"
	GitCommit = "unknown"
	Dirty = "false"
	if got := String(); got != "dev" {
		t.Fatalf("got %q, want %q", got, "dev")
	}
}

func TestStringClean(t *testing.T) {
	Version = "0.1.0"
	GitCommit = "abc1234"
	Dirty = "false"
	if got := String(); got != "0.1.0 (abc1234)" {
		t.Fatalf("got %q", got)
	}
}

func TestStringDirty(t *testing.T) {
	Version = "0.1.0"
	GitCommit = "abc1234"
	Dirty = "true"
	if got := String(); got != "0.1.0 (abc1234, dirty)" {
		t.Fatalf("got %q", got)
	}
}
