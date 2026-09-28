package deletehold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTriggered(t *testing.T) {
	cases := []struct {
		name                    string
		tracked, missing, stake int
		want                    bool
	}{
		{"nothing tracked", 0, 0, 0, false},
		{"nothing missing", 5, 0, 0, false},
		{"single file project emptied", 1, 1, 1, false},
		{"two file project emptied", 2, 2, 2, true},
		{"every file gone, only one still in the store", 5, 5, 1, false},
		{"every file gone, none in the store", 5, 5, 0, false},
		{"a few deletes in a small project", 8, 3, 3, false},
		{"ten of twenty", 20, 10, 10, true},
		{"nine of twelve: under the minimum", 12, 9, 9, false},
		{"twelve of twenty-five: under half", 25, 12, 12, false},
		{"thirteen of twenty-five", 25, 13, 13, true},
		{"missing but mostly already gone from the store", 30, 20, 5, false},
	}
	for _, c := range cases {
		if got := Triggered(c.tracked, c.missing, c.stake); got != c.want {
			t.Errorf("%s: Triggered(%d, %d, %d) = %v, want %v",
				c.name, c.tracked, c.missing, c.stake, got, c.want)
		}
	}
}

func TestNewHoldSamplesAndKeepsFirstSeen(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "holds", "p.json")
	var held []string
	for i := 0; i < 15; i++ {
		held = append(held, string(rune('o'-i))+".md")
	}
	first := time.Date(2026, 5, 12, 23, 6, 0, 0, time.UTC)

	h := NewHold(marker, held, first)
	if !h.New || h.Count != 15 || len(h.Sample) != SampleSize {
		t.Fatalf("unexpected first hold: %+v", h)
	}
	if h.Sample[0] != "a.md" {
		t.Errorf("sample should be sorted, got %v", h.Sample)
	}
	if err := Write(marker, h); err != nil {
		t.Fatal(err)
	}

	later := NewHold(marker, held[:3], first.Add(2*time.Hour))
	if later.New {
		t.Error("a hold that already has a marker is not new")
	}
	if !later.FirstSeen.Equal(first) {
		t.Errorf("FirstSeen should carry over, got %v", later.FirstSeen)
	}
	if later.Count != 3 {
		t.Errorf("Count should reflect this pass, got %d", later.Count)
	}

	if err := Remove(marker); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(marker); ok {
		t.Error("marker should be gone after Remove")
	}
	if err := Remove(marker); err != nil {
		t.Errorf("removing an absent marker is not an error: %v", err)
	}
}

func TestReadRejectsGarbage(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(marker, []byte("not json"), 0o644)
	if _, ok := Read(marker); ok {
		t.Error("an unparseable marker should read as absent")
	}
}

func TestWaysOut(t *testing.T) {
	rc := strings.Join(WaysOut("rclone", "/store/p"), "\n")
	if !strings.Contains(rc, "swarf pull") || !strings.Contains(rc, "--allow-deletes") {
		t.Errorf("rclone ways out should name pull and --allow-deletes:\n%s", rc)
	}
	git := strings.Join(WaysOut("git", "/store/p"), "\n")
	if !strings.Contains(git, "swarf pull") || !strings.Contains(git, "--allow-deletes") {
		t.Errorf("git ways out should name pull and --allow-deletes:\n%s", git)
	}
	other := strings.Join(WaysOut("other", "/store/p"), "\n")
	if strings.Contains(other, "swarf pull") || !strings.Contains(other, "/store/p") {
		t.Errorf("a backend with no pull should point at the store, not pull:\n%s", other)
	}
}
