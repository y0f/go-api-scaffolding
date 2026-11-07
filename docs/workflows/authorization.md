# Protect an operation

Authentication verifies a bearer token. Authorization decides which operations
its principal may perform.

1. Declare `bearerAuth` security for the operation in `api/openapi.yaml`.
2. Define a permission such as `notes:write` or `notes:read`.
3. Pass the principal to the service and check permission before persistence.
4. Grant access through token scopes or explicit entries in
   `internal/auth/principal.go`. An `admin` role has only its listed grants.
5. Test no token (401), a token without permission (403), and an authorized
   token. Verify denied calls do not modify data.
6. Regenerate, then run unit and HTTP integration tests.

Generated reads are public. If the data needs protection, change the contract,
service signatures, tests, and client examples together.

<!-- forge:begin idempotency -->
Idempotent creates check the caller's current write permission before returning
a stored response. Keep this check when changing replay behavior, and test
permission changes between the original request and its retry.
<!-- forge:end idempotency -->
