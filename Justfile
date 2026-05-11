default:
    @just --list

VERSION := `git describe --tags 2>/dev/null || echo dev`
GIT_COMMIT := `git rev-parse --short HEAD 2>/dev/null || echo unknown`
DIRTY := `test -z "$(git status --porcelain 2>/dev/null)" && echo false || echo true`
LDFLAGS := "-X github.com/mschulkind-oss/swarf/internal/version.Version=" + VERSION + " -X github.com/mschulkind-oss/swarf/internal/version.GitCommit=" + GIT_COMMIT + " -X github.com/mschulkind-oss/swarf/internal/version.Dirty=" + DIRTY

check: lint test

lint:
    go vet ./...

test *ARGS:
    go test ./... {{ ARGS }}

test-fast *ARGS:
    go test ./... -count=1 -short {{ ARGS }}

build:
    @mkdir -p dist
    go build -ldflags '{{ LDFLAGS }}' -o dist/swarf .

install: build
    rm -f ~/.local/bin/swarf
    cp dist/swarf ~/.local/bin/swarf

# Build, install, and restart the daemon. Re-runs 'swarf daemon install' so
# the systemd unit's ExecStart always matches the binary we just placed —
# otherwise a reinstall from a different source (brew, go install, etc.)
# leaves the unit pointing at a path that no longer exists and systemd
# fails with status=203/EXEC on every restart.
deploy: build install
    ~/.local/bin/swarf daemon install
    @echo "swarf deployed. Verify: swarf --version"

# Clean build artifacts
clean:
    rm -rf dist
    go clean -cache

# Restart the systemd user service
restart-service:
    systemctl --user restart swarf

# Show systemd service status
status-service:
    systemctl --user status swarf

# Follow daemon logs via journald
logs:
    journalctl --user -u swarf -f
