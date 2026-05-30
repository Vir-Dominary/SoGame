package plugins

import (
	"netjoin/internal/plugin"
	"netjoin/internal/plugins/civ6"
)

// All returns all built-in plugins registered at compile time.
func All() []plugin.Plugin {
	return []plugin.Plugin{
		civ6.New(),
	}
}
