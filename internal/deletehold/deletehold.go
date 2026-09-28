// Package deletehold decides when the forward mirror must hold its
// deletes, and records the hold where the daemon, `swarf status`, and
// `swarf doctor` can see it.
//
// A delete hold is the state a project is in when the forward mirror
// found that applying the deletes it computed would remove a
// suspiciously large share of the project's tracked files, and so
// applied none of them. The held paths stay in the manifest, so the hold
// persists until the files come back or the user confirms the deletes
// with `swarf push --allow-deletes`.
//
// The hold marker is the file ~/.cache/swarf/delete-holds/<slug>.json
// (paths.DeleteHoldMarker) that records a delete hold. It is removed as
// soon as a forward pass completes without holding.
package deletehold

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// WipeMinimum is the fewest files at stake for a whole-project wipe
	// to hold. A project that tracked one file and lost it is an
	// ordinary delete.
	WipeMinimum = 2
	// MassMinimum is the fewest files at stake for the "half the
	// manifest" rule to hold, so small projects can still delete a few
	// files in one go.
	MassMinimum = 10
	// SampleSize caps how many held paths the hold marker lists.
	SampleSize = 10
)

// Triggered reports whether a forward pass must hold its deletes.
//
// tracked is the number of manifest entries the forward mirror is
// responsible for (sync excludes left out), missing is how many of those
// are gone from the project, and atStake is how many of the missing ones
// still exist in the store, which is what a delete would actually remove.
//
// Two shapes hold: the project lost every tracked file and at least
// WipeMinimum are at stake, or at least MassMinimum files are at stake
// and they are at least half of what the manifest tracks.
func Triggered(tracked, missing, atStake int) bool {
	if tracked == 0 || atStake == 0 {
		return false
	}
	if missing == tracked && atStake >= WipeMinimum {
		return true
	}
	return atStake >= MassMinimum && 2*atStake >= tracked
}

// Hold is the content of a hold marker.
type Hold struct {
	// Count is the number of deletes being held.
	Count int `json:"count"`
	// Sample lists up to SampleSize of the held paths, sorted, relative
	// to the project's swarf/ directory.
	Sample []string `json:"sample"`
	// FirstSeen is when the hold started. It survives later passes
	// that hold again, so it says how long the project has been held.
	FirstSeen time.Time `json:"first_seen"`

	// New is true when this pass started the hold: no marker existed
	// before it. Not stored; it is how the daemon logs a hold once.
	New bool `json:"-"`
}

// NewHold builds the hold for this pass from the held paths, carrying
// FirstSeen over from the marker at markerPath when one exists.
func NewHold(markerPath string, held []string, now time.Time) *Hold {
	sorted := append([]string(nil), held...)
	sort.Strings(sorted)
	sample := sorted
	if len(sample) > SampleSize {
		sample = sample[:SampleSize]
	}
	h := &Hold{Count: len(held), Sample: sample, FirstSeen: now, New: true}
	if markerPath != "" {
		if prev, ok := Read(markerPath); ok {
			h.FirstSeen = prev.FirstSeen
			h.New = false
		}
	}
	return h
}

// Read loads the hold marker at path. ok is false when there is no
// marker, or it cannot be parsed.
func Read(path string) (*Hold, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var h Hold
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, false
	}
	return &h, true
}

// Write records h as the hold marker at path.
func Write(path string, h *Hold) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Remove deletes the hold marker at path, if there is one.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Summary is the one-line description of a hold for slug.
func (h *Hold) Summary(slug string) string {
	return fmt.Sprintf("%s: delete hold — %d tracked file(s) missing from the project were not deleted from the store (since %s)",
		slug, h.Count, h.FirstSeen.Local().Format("2006-01-02 15:04"))
}

// WaysOut lists the two ways to end a hold, worded for the configured
// backend: restore the files, or confirm the deletes. On the rclone and
// git backends 'swarf pull' restores them (its reverse mirror copies the
// held files back from the store); otherwise the user copies them.
func WaysOut(backend, storeProjectDir string) []string {
	restore := fmt.Sprintf("Restore the files: copy them back from the store at %s", storeProjectDir)
	if backend == "rclone" || backend == "git" {
		restore = "Restore the files: 'swarf pull' copies them back from the store"
	}
	return []string{
		restore,
		"Or confirm the deletes: 'swarf push --allow-deletes'",
	}
}
