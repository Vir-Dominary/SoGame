package plugin

// Session is the networking context shared with all plugins.
type Session struct {
	Connected bool
	IsHost    bool
	MyIP      string
	HostIP    string
	Community string
	Supernode string
}
