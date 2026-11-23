// Package web contains the public demo's HTML and assets. Only these directories
// are bundled; configuration, private records, and admin source are never served.
package web

import "embed"

//go:embed pages assets
var Files embed.FS
