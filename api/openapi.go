// Package api holds the OpenAPI contract. The server interface and models are
// generated from openapi.yaml into internal/gen/api; this package embeds the
// file itself so the running service can hand it out verbatim.
package api

import _ "embed"

// OpenAPI is api/openapi.yaml as committed: the document clients, docs and
// generators all read.
//
//go:embed openapi.yaml
var OpenAPI []byte
