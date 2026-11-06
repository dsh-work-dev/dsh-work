package settings

import (
	"context"
	"errors"
	"sort"
)

var ComputerRiskCategories = []string{"payment", "send", "publish", "delete", "install", "settings", "command", "close", "submit", "save", "unknown"}

type ComputerAppRule struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Level string `json:"level"`
}
type ComputerPolicy struct {
	NewApps    string            `json:"newApps"`
	GrantLasts string            `json:"grantLasts"`
	Apps       []ComputerAppRule `json:"apps"`
	Confirm    map[string]string `json:"confirm"`
}

func DefaultComputerPolicy() ComputerPolicy {
	p := ComputerPolicy{NewApps: "ask", GrantLasts: "conversation", Apps: []ComputerAppRule{}, Confirm: map[string]string{}}
	for _, c := range ComputerRiskCategories {
		p.Confirm[c] = "ask"
	}
	return p
}
func NormalizeComputerPolicy(in *ComputerPolicy) ComputerPolicy {
	p := DefaultComputerPolicy()
	if in == nil {
		return p
	}
	if in.NewApps == "allow" {
		p.NewApps = "allow"
	}
	if in.GrantLasts == "turn" {
		p.GrantLasts = "turn"
	}
	for _, c := range ComputerRiskCategories {
		v := in.Confirm[c]
		if v == "block" || v == "allow" && c != "payment" {
			p.Confirm[c] = v
		}
	}
	seen := map[string]bool{}
	for _, a := range in.Apps {
		if a.ID != "" && !seen[a.ID] && (a.Level == "view" || a.Level == "click" || a.Level == "full" || a.Level == "blocked") {
			p.Apps = append(p.Apps, a)
			seen[a.ID] = true
		}
	}
	sort.Slice(p.Apps, func(i, j int) bool { return p.Apps[i].ID < p.Apps[j].ID })
	return p
}
func (m *Manager) SetComputerPolicy(ctx context.Context, p ComputerPolicy) (Values, error) {
	if e := contextError(ctx); e != nil {
		return Values{}, e
	}
	if p.NewApps != "allow" && p.NewApps != "ask" || p.GrantLasts != "turn" && p.GrantLasts != "conversation" || len(p.Apps) > 256 {
		return Values{}, errors.New("COMPUTER_POLICY_INVALID")
	}
	normalized := NormalizeComputerPolicy(&p)
	if len(normalized.Apps) != len(p.Apps) {
		return Values{}, errors.New("COMPUTER_POLICY_INVALID")
	}
	for c, v := range p.Confirm {
		if _, ok := normalized.Confirm[c]; !ok || v != "allow" && v != "ask" && v != "block" || c == "payment" && v == "allow" {
			return Values{}, errors.New("COMPUTER_POLICY_INVALID")
		}
	}
	m.mu.Lock()
	next := m.values
	next.Automation.Computer.Policy = &normalized
	if e := m.store.Save(ctx, m.path, next); e != nil {
		m.mu.Unlock()
		return Values{}, e
	}
	return m.commit(next), nil
}
