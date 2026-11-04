package settings

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/local/dsh-work/internal/logging"
	"golang.org/x/net/idna"
)

var BrowserRiskCategories = []string{"payment", "send", "publish", "delete", "install", "settings", "submit", "upload", "script", "unknown"}

type BrowserSiteRule struct {
	Host string `json:"host"`
	Rule string `json:"rule"`
}
type BrowserPolicy struct {
	AskEachConversation bool              `json:"askEachConversation"`
	NewSites            string            `json:"newSites"`
	Sites               []BrowserSiteRule `json:"sites"`
	Confirm             map[string]string `json:"confirm"`
	GrantLasts          string            `json:"grantLasts"`
}

func DefaultBrowserPolicy() BrowserPolicy {
	p := BrowserPolicy{AskEachConversation: true, NewSites: "allow", Sites: []BrowserSiteRule{}, Confirm: map[string]string{}, GrantLasts: "conversation"}
	for _, c := range BrowserRiskCategories {
		p.Confirm[c] = "ask"
	}
	return p
}
func (p *BrowserPolicy) UnmarshalJSON(data []byte) error {
	type wire BrowserPolicy
	*p = DefaultBrowserPolicy()
	v := wire(DefaultBrowserPolicy())
	if err := json.Unmarshal(data, &v); err != nil {
		logging.Module(logging.ModSettings).Warn("browser policy used defaults for invalid values", "code", "BROWSER_POLICY_INVALID")
		return nil
	}
	*p = NormalizeBrowserPolicy((*BrowserPolicy)(&v))
	return nil
}
func NormalizeBrowserPolicy(input *BrowserPolicy) BrowserPolicy {
	p := DefaultBrowserPolicy()
	if input == nil {
		return p
	}
	p.AskEachConversation = input.AskEachConversation
	if input.NewSites == "ask" {
		p.NewSites = "ask"
	}
	if input.GrantLasts == "turn" {
		p.GrantLasts = "turn"
	}
	for _, c := range BrowserRiskCategories {
		v := input.Confirm[c]
		if v == "block" || v == "allow" && c != "payment" {
			p.Confirm[c] = v
		}
	}
	seen := map[string]int{}
	for _, s := range input.Sites {
		host, e := NormalizeBrowserHost(s.Host)
		if e != nil || (s.Rule != "allow" && s.Rule != "block") {
			continue
		}
		if i, ok := seen[host]; ok {
			if s.Rule == "block" {
				p.Sites[i].Rule = "block"
			}
			continue
		}
		seen[host] = len(p.Sites)
		p.Sites = append(p.Sites, BrowserSiteRule{host, s.Rule})
	}
	sort.Slice(p.Sites, func(i, j int) bool { return p.Sites[i].Host < p.Sites[j].Host })
	return p
}
func NormalizeBrowserHost(value string) (string, error) { return normalizeBrowserHost(value, true) }
func BrowserAddressHost(value string) (string, error)   { return normalizeBrowserHost(value, false) }
func normalizeBrowserHost(value string, siteRule bool) (string, error) {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	u, e := url.Parse(value)
	if e != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("BROWSER_SITE_INVALID")
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	h = strings.TrimPrefix(strings.TrimPrefix(h, "*."), "www.")
	if ip := net.ParseIP(h); ip != nil {
		if siteRule && ip.To4() == nil {
			return "", errors.New("BROWSER_SITE_INVALID")
		}
		return ip.String(), nil
	}
	h, e = idna.Lookup.ToASCII(h)
	if e != nil || h == "" || len(h) > 253 {
		return "", errors.New("BROWSER_SITE_INVALID")
	}
	if h == "localhost" || net.ParseIP(h).To4() != nil {
		return h, nil
	}
	if siteRule && !strings.Contains(h, ".") {
		return "", errors.New("BROWSER_SITE_INVALID")
	}
	for _, label := range strings.Split(h, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("BROWSER_SITE_INVALID")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", errors.New("BROWSER_SITE_INVALID")
			}
		}
	}
	return h, nil
}
func (p BrowserPolicy) SiteRule(host string) string {
	best, result := 0, ""
	for _, s := range p.Sites {
		if host == s.Host || strings.HasSuffix(host, "."+s.Host) {
			if len(s.Host) > best || len(s.Host) == best && s.Rule == "block" {
				best, result = len(s.Host), s.Rule
			}
		}
	}
	return result
}
func (m *Manager) SetBrowserPolicy(ctx context.Context, p BrowserPolicy) (Values, error) {
	if err := contextError(ctx); err != nil {
		return Values{}, err
	}
	if (p.NewSites != "allow" && p.NewSites != "ask") || (p.GrantLasts != "turn" && p.GrantLasts != "conversation") || len(p.Sites) > 256 {
		return Values{}, errors.New("BROWSER_POLICY_INVALID")
	}
	for c, v := range p.Confirm {
		known := false
		for _, key := range BrowserRiskCategories {
			known = known || c == key
		}
		if !known || (v != "ask" && v != "allow" && v != "block") || c == "payment" && v == "allow" {
			return Values{}, errors.New("BROWSER_POLICY_INVALID")
		}
	}
	seen := map[string]bool{}
	for _, s := range p.Sites {
		host, e := NormalizeBrowserHost(s.Host)
		if e != nil {
			return Values{}, e
		}
		if seen[host] {
			return Values{}, errors.New("BROWSER_SITE_DUPLICATE")
		}
		seen[host] = true
		if s.Rule != "allow" && s.Rule != "block" {
			return Values{}, errors.New("BROWSER_POLICY_INVALID")
		}
	}
	p = NormalizeBrowserPolicy(&p)
	m.mu.Lock()
	next := m.values
	next.Automation.Browser.Policy = &p
	if err := m.store.Save(ctx, m.path, next); err != nil {
		m.mu.Unlock()
		return Values{}, err
	}
	return m.commit(next), nil
}
