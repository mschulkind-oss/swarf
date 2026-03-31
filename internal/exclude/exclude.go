package exclude

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	fenceStart = "# --- swarf managed (do not edit) ---"
	fenceEnd   = "# --- end swarf ---"
)

var baseExcludes = []string{"/.swarf/", "/.mise.local.toml"}

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
	all := append(append([]string{}, baseExcludes...), current...)
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
