// Package mynotes holds repository-level embedded assets.
package mynotes

import _ "embed"

// OpenAPI is the API description served at /openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
