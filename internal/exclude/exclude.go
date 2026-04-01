package exclude

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

const (
	fenceStart = "# --- swarf managed (do not edit) ---"
	fenceEnd   = "# --- end swarf ---"
)

// BaseExcludes returns the default exclude entries using the current dir name.
func BaseExcludes() []string {
	return []string{"/" + paths.SwarfDirName + "/"}
}

func excludeFile(hostRoot string) string {
	return filepath.Join(hostRoot, ".git", "info", "exclude")
}

func ReadManagedExcludes(hostRoot string) []string {
	data, err := os.ReadFile(excludeFile(hostRoot))
	if err != nil {
		return nil
	}
	var entries []string
	inFence := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == fenceStart {
			inFence = true
			continue
		}
		if trimmed == fenceEnd {
			inFence = false
			continue
		}
		if inFence && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			entries = append(entries, trimmed)
		}
	}
	return entries
}

func WriteManagedExcludes(hostRoot string, entries []string) error {
	path := excludeFile(hostRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Read existing content, stripping old fenced section
	var existing []string
	if data, err := os.ReadFile(path); err == nil {
		inFence := false
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == fenceStart {
				inFence = true
				continue
			}
			if trimmed == fenceEnd {
				inFence = false
				continue
			}
			if !inFence {
				existing = append(existing, line)
			}
		}
	}

	// Remove trailing blank lines
	for len(existing) > 0 && strings.TrimSpace(existing[len(existing)-1]) == "" {
		existing = existing[:len(existing)-1]
	}

	// Dedupe and sort entries
	unique := dedupe(entries)
	slices.Sort(unique)

	var parts []string
	parts = append(parts, existing...)
	if len(parts) > 0 {
		parts = append(parts, "")
	}
	parts = append(parts, fenceStart)
	parts = append(parts, unique...)
	parts = append(parts, fenceEnd)
	parts = append(parts, "")

	return os.WriteFile(path, []byte(strings.Join(parts, "\n")), 0o644)
}

func UpdateExcludes(hostRoot string, extra []string) error {
	current := ReadManagedExcludes(hostRoot)
	all := append(append([]string{}, BaseExcludes()...), current...)
	all = append(all, extra...)
	return WriteManagedExcludes(hostRoot, all)
}

func AddLinkedExcludes(hostRoot string, linkedPaths []string) error {
	if len(linkedPaths) == 0 {
		return nil
	}
	extra := make([]string, len(linkedPaths))
	for i, p := range linkedPaths {
		if !strings.HasPrefix(p, "/") {
			extra[i] = "/" + p
		} else {
			extra[i] = p
		}
	}
	return UpdateExcludes(hostRoot, extra)
}

// RemoveExcludes removes the given entries from the managed exclude section.
func RemoveExcludes(hostRoot string, entries []string) error {
	if len(entries) == 0 {
		return nil
	}
	remove := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e, "/") {
			e = "/" + e
		}
		remove[e] = true
	}
	current := ReadManagedExcludes(hostRoot)
	var kept []string
	for _, e := range current {
		if !remove[e] {
			kept = append(kept, e)
		}
	}
	return WriteManagedExcludes(hostRoot, kept)
}

func dedupe(s []string) []string {
	seen := make(map[string]bool, len(s))
	var result []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
