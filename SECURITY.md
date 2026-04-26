# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | ✅        |

## Reporting a Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

Instead, report security issues by emailing the maintainer directly (see the GitHub profile for contact details) or via [GitHub private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability).

Please include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Any suggested mitigations

We aim to respond within 72 hours and will coordinate disclosure after a fix is available.

## Security considerations

Praetor runs as an unprivileged system user (`praetor`) and communicates over mTLS. The agent binary is signed with [cosign keyless](https://docs.sigstore.dev/). SBOM is attached to each release.
