package server

import (
	"net/http"
	"strconv"
)

// The reference UI is Scalar, loaded from a CDN so the binary carries no
// front-end assets and the page follows the spec served next to it. Pin the
// version in the URL if a reproducible page matters more than staying current.
const docsPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>API reference</title>
</head>
<body>
<script id="api-reference" data-url="/openapi.yaml"></script>
<script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>
`

// SpecHandler serves the OpenAPI document verbatim, so clients and generators
// consume exactly the contract the running server validates against.
func SpecHandler(spec []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("Content-Length", strconv.Itoa(len(spec)))
		_, _ = w.Write(spec)
	}
}

// DocsHandler serves the interactive API reference for the spec at
// /openapi.yaml.
func DocsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(docsPage))
}
