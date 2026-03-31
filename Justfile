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
    go build -ldflags '{{ LDFLAGS }}' -o swarf .

install: build
    cp swarf ~/.local/bin/swarf

# Build and install locally
deploy: build install
    @echo "swarf deployed. Verify: swarf --version"

# Clean build artifacts
clean:
    rm -f swarf
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
