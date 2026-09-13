// Package migrations embeds the versioned SQL migration files so they ship
// inside the binary and can be applied without the source tree present.
package migrations

import "embed"

// Every file rather than *.sql: an embed pattern that matches nothing is a
// compile error, and a project has no migrations until its first resource.
// goose only runs files with a numeric version prefix, so this file is inert.
//
//go:embed *
var FS embed.FS
