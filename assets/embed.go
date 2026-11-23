// Package assets holds files compiled into the binary.
package assets

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte
