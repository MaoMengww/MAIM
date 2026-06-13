// Package postgres embeds the PostgreSQL migration SQL files so they are
// compiled into the binary and can be applied automatically at startup.
package postgres

import "embed"

//go:embed *.sql
var FS embed.FS
