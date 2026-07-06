// Package deps pins module dependencies that later tasks wire in, so that
// `go mod tidy` does not drop them before their first real use. Delete an entry
// here once its real import lands.
package deps

import (
	// Pinned for task 1.5 (componentregistry.RegisterAll in both binaries) and
	// the capability groups. This blank import also validates that semstreams
	// resolves via the local replace and compiles inside semdev's module.
	_ "github.com/c360studio/semstreams/component"
)
