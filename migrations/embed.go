package migrations

import "embed"

// Files contains the ordered SQL migrations applied by the SQLite store.
//
//go:embed *.sql
var Files embed.FS
