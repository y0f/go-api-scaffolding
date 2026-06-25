// Package migrations embeds the versioned SQL migration files so they ship
// inside the binary and can be applied without the source tree present.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
