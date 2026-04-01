package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/mschulkind-oss/swarf/internal/paths"
)

type GlobalConfig struct {
	Backend   string   `toml:"backend"`
	Remote    string   `toml:"remote"`
	Debounce  string   `toml:"debounce"`
	DirName   string   `toml:"dir_name"`
	AutoSweep []string `toml:"-"`
}

type globalConfigFile struct {
	Sync      syncSection      `toml:"sync"`
	AutoSweep autoSweepSection `toml:"auto_sweep,omitempty"`
}

type syncSection struct {
	Backend  string `toml:"backend"`
	Remote   string `toml:"remote"`
	Debounce string `toml:"debounce"`
	DirName  string `toml:"dir_name,omitempty"`
}

type autoSweepSection struct {
	Paths []string `toml:"paths,omitempty"`
}

func ReadGlobalConfig() *GlobalConfig {
	data, err := os.ReadFile(paths.GlobalConfigTOML)
	if err != nil {
		return nil
	}
	var f globalConfigFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil
	}
	c := &GlobalConfig{
		Backend:  f.Sync.Backend,
		Remote:   f.Sync.Remote,
		Debounce: f.Sync.Debounce,
		DirName:  f.Sync.DirName,
	}
	if c.Backend == "" {
		c.Backend = "git"
	}
	if c.Debounce == "" {
		c.Debounce = "5s"
	}
	if c.DirName != "" {
		paths.SwarfDirName = c.DirName
	}
	c.AutoSweep = f.AutoSweep.Paths
	return c
}

func WriteGlobalConfig(c *GlobalConfig) error {
	if err := os.MkdirAll(paths.ConfigDir, 0o755); err != nil {
		return err
	}
	f := globalConfigFile{
		Sync: syncSection{
			Backend:  c.Backend,
			Remote:   c.Remote,
			Debounce: c.Debounce,
			DirName:  c.DirName,
		},
	}
	if len(c.AutoSweep) > 0 {
		f.AutoSweep = autoSweepSection{Paths: c.AutoSweep}
	}
	data, err := toml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(paths.GlobalConfigTOML, data, 0o644)
}

type DrawerEntry struct {
	Slug string `toml:"slug"`
	Host string `toml:"host"`
}

type drawersFile struct {
	Drawers []DrawerEntry `toml:"drawers"`
}

func ReadDrawers() []DrawerEntry {
	data, err := os.ReadFile(paths.DrawersTOML)
	if err != nil {
		return nil
	}
	var f drawersFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil
	}
	return f.Drawers
}

func RegisterDrawer(slug, host string) error {
	if err := os.MkdirAll(paths.ConfigDir, 0o755); err != nil {
		return err
	}
	abs, _ := filepath.Abs(host)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	drawers := ReadDrawers()
	for i, d := range drawers {
		if d.Slug == slug {
			drawers[i].Host = abs
			return writeDrawers(drawers)
		}
	}
	drawers = append(drawers, DrawerEntry{Slug: slug, Host: abs})
	return writeDrawers(drawers)
}

func UnregisterDrawer(slug string) error {
	drawers := ReadDrawers()
	filtered := make([]DrawerEntry, 0, len(drawers))
	for _, d := range drawers {
		if d.Slug != slug {
			filtered = append(filtered, d)
		}
	}
	return writeDrawers(filtered)
}

func writeDrawers(drawers []DrawerEntry) error {
	if err := os.MkdirAll(paths.ConfigDir, 0o755); err != nil {
		return err
	}
	f := drawersFile{Drawers: drawers}
	data, err := toml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(paths.DrawersTOML, data, 0o644)
}

var durationRE = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(ms|s|m|h)$`)

func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	m := durationRE.FindStringSubmatch(s)
	if m == nil {
		return 0, &strconv.NumError{Func: "ParseDuration", Num: s, Err: strconv.ErrSyntax}
	}
	val, _ := strconv.ParseFloat(m[1], 64)
	switch m[2] {
	case "ms":
		return time.Duration(val * float64(time.Millisecond)), nil
	case "s":
		return time.Duration(val * float64(time.Second)), nil
	case "m":
		return time.Duration(val * float64(time.Minute)), nil
	case "h":
		return time.Duration(val * float64(time.Hour)), nil
	}
	return 0, &strconv.NumError{Func: "ParseDuration", Num: s, Err: strconv.ErrSyntax}
}
