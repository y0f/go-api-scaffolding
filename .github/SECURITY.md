# Security Policy

## Reporting a vulnerability

Report vulnerabilities privately through this repository's **Security** tab,
then **Report a vulnerability**. If that option is unavailable, use a private
contact explicitly provided by the maintainer. Do not put vulnerability details
in a public issue.
<!-- forge:begin init -->
For this scaffold, use [private vulnerability reporting](https://github.com/y0f/gophling/security/advisories/new).
<!-- forge:end init -->

Include reproduction steps and the affected version. You can expect an
acknowledgement within a few days.

## What this service does

- `govulncheck` runs in CI and fails on reachable vulnerabilities.
- GitHub Actions are pinned to commit SHAs, and Dependabot keeps them current.
- Release checksums are signed with cosign keyless signing; archives ship an SBOM.
- Uploaded artifacts, signatures, and applicable provenance are verified while
  the release is still a draft. Published immutable releases are verified on
  reruns without replacing assets. See [the release workflow](RELEASING.md).
- Public GitHub.com releases publish build provenance attestations identifying
  the workflow and source revision. See [release verification](../README.md#verify-a-release).
- Bearer tokens are verified against JWKS (OIDC) or a configured public key;
  the development fallback key is never used outside the development environment.
- Logs pass through a redaction handler that scrubs known sensitive keys.
- `X-Forwarded-For` is ignored unless the connecting peer is in
  `FORGE_HTTP_TRUSTED_PROXIES`, so a client cannot spoof its address to the
  rate limiter or the access log. The `client.address` attribute on traces is
  set by otelhttp from the raw header and is not trust-filtered; rely on
  `network.peer.address` or the access log instead.

These reduce risk but do not replace your own review. Audit the auth, CORS, and
rate-limit settings before deploying.
