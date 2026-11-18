// Package static embeds the CSS and JavaScript served at /static/.
package static

import "embed"

//go:embed js/*.js css/*.css
var FS embed.FS
