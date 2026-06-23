package plugins

import (
	"sogame/internal/plugin"
	"sogame/internal/plugins/civ6"
)

// All returns all built-in plugins registered at compile time.
func All() []plugin.Plugin {
	return []plugin.Plugin{
		civ6.New(),
	}
}
