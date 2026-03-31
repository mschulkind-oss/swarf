package version

import "fmt"

// Set via ldflags at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	Dirty     = "false"
)

func String() string {
	if GitCommit == "unknown" {
		return Version
	}
	if Dirty == "true" {
		return fmt.Sprintf("%s (%s, dirty)", Version, GitCommit)
	}
	return fmt.Sprintf("%s (%s)", Version, GitCommit)
}
