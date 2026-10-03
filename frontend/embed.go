// Package frontend embeds the built admin web (npm run build writes dist/).
package frontend

import "embed"

// Dist is the build; it holds only .gitkeep in a checkout that was never
// built, and the binary then serves no admin web.
//
//go:embed all:dist
var Dist embed.FS
