package plugin

const (
	StateIdle        = "idle"
	StateReady       = "ready"
	StateActive      = "active"
	StateError       = "error"
	StateUnsupported = "unsupported"

	// 标准操作 ID，大多数插件都会用到
	ActionActivate   = "activate"
	ActionDeactivate = "deactivate"
)

// Plugin 是利用虚拟网络会话的游戏或工具扩展。
//
// 每个插件在 ExecuteAction 中自行处理自己的操作。两个标准操作为
// "activate" 和 "deactivate"，插件也可以定义额外的自定义 action ID
// （例如 "start-server"、"configure" 等）。
type Plugin interface {
	Meta() Meta
	Ready() bool
	CanActivate(session Session) (bool, string)
	Status(session Session) Status
	ExecuteAction(actionID string, session Session) error
}

type Meta struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Game          string `json:"game"`
	RuntimeDir    string `json:"runtime_dir"`
	RequiresAdmin bool   `json:"requires_admin"`
}

type Status struct {
	State   string            `json:"state"`
	Message string            `json:"message"`
	Details map[string]string `json:"details"`
	Actions []Action          `json:"actions"`
}

type Action struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}
