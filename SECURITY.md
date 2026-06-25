# Security Policy

## Reporting a vulnerability

Please report security issues privately through GitHub's
[private vulnerability reporting](https://github.com/y0f/go-api-scaffolding/security/advisories/new)
rather than opening a public issue. Include reproduction steps and the affected
version. You can expect an acknowledgement within a few days.

## What this scaffold gives you

- `govulncheck` runs in CI and fails on reachable vulnerabilities.
- GitHub Actions are pinned to commit SHAs, and Dependabot keeps them current.
- Released binaries are signed with cosign keyless signing and ship an SBOM.
- Bearer tokens are verified against JWKS (OIDC) or a configured public key;
  the development fallback key is never used outside the development environment.
- Logs pass through a redaction handler that scrubs known sensitive keys.

These reduce risk but do not replace your own review. Audit the auth, CORS, and
rate-limit settings before deploying.
