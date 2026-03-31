default:
    @just --list

setup:
    uv sync --group dev

check: format lint test

format:
    uv run ruff format .

lint:
    uv run ruff check --fix .

test *ARGS:
    uv run pytest tests/ {{ ARGS }}

test-fast *ARGS:
    uv run pytest tests/ -x --no-header -q {{ ARGS }}

typecheck:
    uv run basedpyright

build:
    uv build

install:
    uv tool install . --force

# Build and install locally as a uv tool
deploy: build install
    @echo "swarf deployed. Verify: swarf --version"

# Clean build artifacts
clean:
    rm -rf dist/ build/ src/*.egg-info

# Restart the systemd user service
restart-service:
    systemctl --user restart swarf

# Show systemd service status
status-service:
    systemctl --user status swarf

# Follow daemon logs via journald
logs:
    journalctl --user -u swarf -f
