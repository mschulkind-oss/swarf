package link

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/swarf/internal/console"
	"github.com/mschulkind-oss/swarf/internal/exclude"
	"github.com/mschulkind-oss/swarf/internal/paths"
)

var ErrNoProject = errors.New("not inside a swarf project — run 'swarf init' first")

type Result struct {
	Created  []string
	Skipped  []string
	Warnings []string
}

func Run(hostRoot string, quiet bool) (Result, error) {
	if hostRoot == "" {
		hostRoot = paths.FindHostRoot("")
	}
	if hostRoot == "" {
		return Result{}, ErrNoProject
	}

	ld := paths.LinksDir(hostRoot)
	result := Result{}

	if !paths.IsDir(ld) {
		return result, nil
	}
	entries, err := os.ReadDir(ld)
	if err != nil || len(entries) == 0 {
		return result, nil
	}

	filepath.Walk(ld, func(source string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(ld, source)
		target := filepath.Join(hostRoot, rel)
		result.processLink(source, target, rel, quiet)
		return nil
	})

	if quiet {
		for _, msg := range result.Warnings {
			console.Warn(msg)
		}
	}

	allLinked := append(result.Created, result.Skipped...)
	if len(allLinked) > 0 {
		normalized := make([]string, len(allLinked))
		for i, p := range allLinked {
			normalized[i] = strings.ReplaceAll(p, "\\", "/")
		}
		exclude.AddLinkedExcludes(hostRoot, normalized)
	}

	return result, nil
}

func (r *Result) processLink(source, target, rel string, quiet bool) {
	fi, err := os.Lstat(target)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		resolved, _ := filepath.EvalSymlinks(target)
		sourceResolved, _ := filepath.EvalSymlinks(source)
		if resolved == sourceResolved {
			r.Skipped = append(r.Skipped, rel)
			return
		}
		os.Remove(target) // stale symlink
	} else if err == nil {
		msg := fmt.Sprintf("%s: real file exists, skipping (won't overwrite)", rel)
		r.Warnings = append(r.Warnings, msg)
		if !quiet {
			console.Warn(msg)
		}
		return
	}

	os.MkdirAll(filepath.Dir(target), 0o755)
	if err := os.Symlink(source, target); err != nil {
		console.Warn(fmt.Sprintf("Failed to link %s: %v", rel, err))
		return
	}
	r.Created = append(r.Created, rel)
	if !quiet {
		console.Infof("  linked %s", rel)
	}
}
