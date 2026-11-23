// Package web holds the single-page app that ships inside the binary.
package web

import "embed"

// FS contains every asset served to the browser. Nothing is loaded from the
// network at runtime.
//
//go:embed index.html styles.css manifest.webmanifest icon.svg icon-180.png js
var FS embed.FS
