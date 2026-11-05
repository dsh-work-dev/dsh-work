package settings

import "context"

// Only implemented preferences are writable. Deferred policy fields are not
// serialized yet; older readers continue to ignore additive settings fields.
type Automation struct {
	Browser  AutomationBrowser  `json:"browser"`
	Computer AutomationComputer `json:"computer"`
}
type AutomationCapability struct {
	Enabled bool `json:"enabled"`
}

type AutomationComputer struct {
	Policy  *ComputerPolicy `json:"policy,omitempty"`
	Enabled bool            `json:"enabled"`
	Engine  string          `json:"engine,omitempty"`
}

func (m *Manager) SetComputerEngine(ctx context.Context, engine string) (Values, error) {
	if engine != "cua-driver" && engine != "auv" {
		return Values{}, failure("SETTINGS_STATE_INVALID", "Unknown computer engine", "choose Cua Driver or AUV")
	}
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	m.mu.Lock()
	next := m.values
	next.Automation.Computer.Engine = engine
	if err := m.store.Save(ctx, m.path, next); err != nil {
		m.mu.Unlock()
		return Values{}, err
	}
	return m.commit(next), nil
}

type AutomationBrowser struct {
	Enabled bool                 `json:"enabled"`
	Default AutomationCapability `json:"default"`
	Policy  *BrowserPolicy       `json:"policy,omitempty"`
}

func DefaultAutomation() Automation {
	return Automation{Browser: AutomationBrowser{Default: AutomationCapability{Enabled: true}}}
}
func (m *Manager) SetAutomationEnabled(ctx context.Context, capability string, enabled bool) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	m.mu.Lock()
	next := m.values
	switch capability {
	case "browser":
		next.Automation.Browser.Enabled = enabled
	case "computer":
		next.Automation.Computer.Enabled = enabled
	default:
		m.mu.Unlock()
		return Values{}, failure("SETTINGS_STATE_INVALID", "Unknown capability", "choose browser or computer")
	}
	if err := m.store.Save(ctx, m.path, next); err != nil {
		m.mu.Unlock()
		return Values{}, err
	}
	return m.commit(next), nil
}

func (m *Manager) SetDefaultBrowserEnabled(ctx context.Context, enabled bool) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	m.mu.Lock()
	next := m.values
	next.Automation.Browser.Default.Enabled = enabled
	if err := m.store.Save(ctx, m.path, next); err != nil {
		m.mu.Unlock()
		return Values{}, err
	}
	return m.commit(next), nil
}
