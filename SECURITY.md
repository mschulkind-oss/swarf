# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Swarf, please report it responsibly.

**Email:** mschulkind@gmail.com

Please include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

## Response Timeline

- **Acknowledgment:** Within 48 hours
- **Initial assessment:** Within 1 week
- **Fix timeline:** Depends on severity, typically within 2 weeks for critical issues

## Scope

Swarf manages personal files alongside git repositories. Security-relevant areas include:

- **Credential leakage** — swarf must never commit or sync credentials, API keys, or tokens
- **Gitignore bypass** — `.swarf/` and linked files must remain invisible to the host repo
- **Remote sync safety** — the daemon must not expose private content to unintended destinations
- **Config injection** — agents must not be able to silently modify swarf configuration

### Out of Scope

- Vulnerabilities in git or rclone themselves
- Issues requiring root access on the host
- Content the user explicitly places in `.swarf/`

## Supported Versions

Only the latest version on the `main` branch is supported with security fixes. There are no LTS releases at this time.

## Disclosure Policy

We follow coordinated disclosure. Please allow reasonable time for a fix before public disclosure.
