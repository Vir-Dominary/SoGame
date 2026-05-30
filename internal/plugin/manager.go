package plugin

import "fmt"

// Manager holds registered plugins and dispatches calls by plugin ID.
type Manager struct {
	plugins map[string]Plugin
	order   []string
}

func NewManager(all ...Plugin) *Manager {
	m := &Manager{
		plugins: make(map[string]Plugin, len(all)),
		order:   make([]string, 0, len(all)),
	}
	for _, p := range all {
		meta := p.Meta()
		if meta.ID == "" {
			continue
		}
		m.plugins[meta.ID] = p
		m.order = append(m.order, meta.ID)
	}
	return m
}

func (m *Manager) Get(id string) (Plugin, error) {
	p, ok := m.plugins[id]
	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", id)
	}
	return p, nil
}

func (m *Manager) ListMeta() []Meta {
	out := make([]Meta, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.plugins[id].Meta())
	}
	return out
}

func (m *Manager) Status(id string, session Session) (Status, error) {
	p, err := m.Get(id)
	if err != nil {
		return Status{}, err
	}
	return p.Status(session), nil
}

func (m *Manager) RunAction(id, actionID string, session Session) error {
	p, err := m.Get(id)
	if err != nil {
		return err
	}
	return p.ExecuteAction(actionID, session)
}
