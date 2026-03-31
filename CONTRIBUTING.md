# Contributing to Swarf

Thank you for your interest in contributing! Here's how to get started.

## Development Setup

### Prerequisites

| Tool | Purpose | Install |
|------|---------|---------|
| [Python 3.13+](https://python.org) | Runtime | Your package manager |
| [uv](https://docs.astral.sh/uv/) | Python package manager | `curl -LsSf https://astral.sh/uv/install.sh \| sh` |
| [just](https://just.systems/) | Command runner | `cargo install just` or `brew install just` |

### Getting Started

```bash
git clone https://github.com/mschulkind-oss/swarf.git
cd swarf
uv sync --group dev   # Install Python dependencies (including dev tools)
just test             # Run tests
just check            # Format, lint, and test
```

### Editable Install (for development)

```bash
uv sync --group dev

# The swarf CLI is available via uv run:
uv run swarf --help

# Or install as a tool for direct CLI access during dev:
uv tool install -e .
```

### Running Tests

```bash
just check    # Format, lint, and test — run this before every PR
just test     # All tests (pytest)
just lint     # Linting only (ruff)
just format   # Auto-format code (ruff)
```

## Making Changes

### Coding Standards

- Type hints on all function signatures
- Follow PEP 8 (enforced by `ruff`)
- Keep functions focused and well-named

### Commit Messages

Use conventional commit style:

```
feat: add rclone backend support
fix: handle missing global gitignore gracefully
docs: update configuration reference
```

## Versioning

Swarf follows [Semantic Versioning](https://semver.org/):

- **MAJOR** (x.0.0) — breaking changes to CLI or config format
- **MINOR** (0.x.0) — new features, backward-compatible
- **PATCH** (0.0.x) — bug fixes, documentation, internal improvements

While in 0.x.y, the API is not considered stable and minor versions may include breaking changes.

## Pull Request Process

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes with tests
4. Run `just check` and ensure everything passes
5. Submit a PR with a clear description of what and why

### What Makes a Good PR

- **Small and focused** — one logical change per PR
- **Tested** — new features have tests, bug fixes include regression tests
- **Documented** — update docs if behavior changes

## Bug Reports

Please include:
- Steps to reproduce
- Expected vs actual behavior
- Swarf version (`swarf --version`)
- OS and Python version

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
