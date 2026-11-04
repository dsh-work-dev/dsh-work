package app

import (
	"context"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/browserhost"
	"github.com/local/dsh-work/internal/settings"
)

type AutomationService struct {
	permissionLocale        func() settings.Locale
	permissionAppearance    func() settings.Appearance
	service                 *automation.Service
	registrar               browserhost.Registrar
	openBrowserExtension    func() error
	chooseBrowserDirectory  func(string) (string, error)
	openBrowserConversation func(context.Context, string) error
}

type BrowserPermissionPanel struct {
	Request    *automation.BrowserConsent `json:"request"`
	Locale     settings.Locale            `json:"locale"`
	Appearance settings.Appearance        `json:"appearance"`
}

// The standalone native window has only permission read/answer authority.
func (s *AutomationService) GetBrowserPermission(ctx context.Context) (BrowserPermissionPanel, error) {
	if !isTrustedWindow(ctx, "browser-permission") {
		return BrowserPermissionPanel{}, trustedSurfaceRequired("Open the browser permission window.")
	}
	panel := BrowserPermissionPanel{Locale: settings.DefaultLocale}
	if s.permissionLocale != nil {
		panel.Locale = s.permissionLocale()
	}
	if s.permissionAppearance != nil {
		panel.Appearance = s.permissionAppearance()
	}
	v := s.service.Snapshot()
	if v.BrowserAccess != nil && len(v.BrowserAccess.Requests) > 0 {
		r := v.BrowserAccess.Requests[0]
		panel.Request = &r
	}
	return panel, nil
}
func SetBrowserPermissionLocale(s *AutomationService, locale func() settings.Locale) {
	s.permissionLocale = locale
}
func SetBrowserPermissionAppearance(s *AutomationService, appearance func() settings.Appearance) {
	s.permissionAppearance = appearance
}
func (s *AutomationService) AnswerBrowserPermission(ctx context.Context, id, choice string) (BrowserPermissionPanel, error) {
	if !isTrustedWindow(ctx, "browser-permission") {
		return BrowserPermissionPanel{}, trustedSurfaceRequired("Open the browser permission window.")
	}
	if _, err := s.service.AnswerBrowserConsent(ctx, id, choice); err != nil {
		return BrowserPermissionPanel{}, err
	}
	return s.GetBrowserPermission(ctx)
}

func NewAutomationService(service *automation.Service) *AutomationService {
	return &AutomationService{service: service}
}
func (s *AutomationService) GetAutomation(ctx context.Context) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work Settings.")
	}
	return s.service.Snapshot(), nil
}
func (s *AutomationService) SetAutomationEnabled(ctx context.Context, capability string, enabled bool) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work Settings.")
	}
	v, e := s.service.SetEnabled(ctx, automation.Capability(capability), enabled)
	if e == nil && capability == "browser" && enabled && v.DefaultBrowserEnabled {
		s.prepareBrowserHost(ctx)
		v = s.service.Snapshot()
	}
	return v, e
}
func (s *AutomationService) RetryAutomation(ctx context.Context, capability string) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work Settings.")
	}
	return s.service.Retry(automation.Capability(capability))
}
func (s *AutomationService) RepairAutomation(ctx context.Context, capability string) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open Browser settings.")
	}
	return s.service.Repair(automation.Capability(capability))
}

func (s *AutomationService) SetDefaultBrowserEnabled(ctx context.Context, enabled bool) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work Settings.")
	}
	v, e := s.service.SetDefaultBrowser(ctx, enabled)
	if e == nil && enabled && v.Browser.Enabled {
		s.prepareBrowserHost(ctx)
		v = s.service.Snapshot()
	}
	return v, e
}

// Setup mutations remain on the existing trusted workbench/current-profile path.
// Native hello and binding candidates themselves never constitute authorization.
func (s *AutomationService) browserSetupAllowed(ctx context.Context) error {
	if !isTrustedWindow(ctx, "workspace") {
		return trustedSurfaceRequired("Open DSH Work Settings.")
	}
	if !s.service.Snapshot().CanChange {
		return automation.ErrRevoked
	}
	return nil
}
func SetBrowserHostRegistrar(s *AutomationService, r browserhost.Registrar) {
	s.registrar = r
	v := s.service.Snapshot()
	if v.Browser.Enabled && v.DefaultBrowserEnabled {
		s.prepareBrowserHost(context.Background())
	}
}
func (s *AutomationService) prepareBrowserHost(ctx context.Context) {
	s.service.BrowserDiscovery(browserhost.Discover())
	if s.registrar != nil {
		s.service.BrowserRegistration(s.registrar.Ensure(ctx))
	}
}
func (s *AutomationService) PrepareBrowserConnection(ctx context.Context) (automation.Snapshot, error) {
	if e := s.browserSetupAllowed(ctx); e != nil {
		return automation.Snapshot{}, e
	}
	s.prepareBrowserHost(ctx)
	return s.service.Snapshot(), nil
}
func (s *AutomationService) ConfirmBrowserBinding(ctx context.Context, id string) (automation.Snapshot, error) {
	if e := s.browserSetupAllowed(ctx); e != nil {
		return automation.Snapshot{}, e
	}
	if _, e := s.service.ConfirmBrowserBinding(ctx, id); e != nil {
		return s.service.Snapshot(), e
	}
	return s.service.Snapshot(), nil
}
func (s *AutomationService) CancelBrowserBinding(ctx context.Context, id string) (automation.Snapshot, error) {
	if e := s.browserSetupAllowed(ctx); e != nil {
		return automation.Snapshot{}, e
	}
	s.service.CancelBrowserBinding(id)
	return s.service.Snapshot(), nil
}
func (s *AutomationService) SelectBrowserBinding(ctx context.Context, id string) (automation.Snapshot, error) {
	if e := s.browserSetupAllowed(ctx); e != nil {
		return automation.Snapshot{}, e
	}
	if e := s.service.SelectBrowserBinding(ctx, id); e != nil {
		return s.service.Snapshot(), e
	}
	return s.service.Snapshot(), nil
}
func (s *AutomationService) ForgetBrowserBinding(ctx context.Context, id string) (automation.Snapshot, error) {
	if e := s.browserSetupAllowed(ctx); e != nil {
		return automation.Snapshot{}, e
	}
	if e := s.service.ForgetBrowserBinding(ctx, id); e != nil {
		return s.service.Snapshot(), e
	}
	return s.service.Snapshot(), nil
}

func (s *AutomationService) RenameBrowserBinding(ctx context.Context, id, name string) (automation.Snapshot, error) {
	if err := s.browserSetupAllowed(ctx); err != nil {
		return automation.Snapshot{}, err
	}
	if err := s.service.RenameBrowserBinding(ctx, id, name); err != nil {
		return s.service.Snapshot(), err
	}
	return s.service.Snapshot(), nil
}

func SetBrowserExtensionFolder(s *AutomationService, open func() error) {
	s.openBrowserExtension = open
}
func (s *AutomationService) OpenBrowserExtensionFolder(ctx context.Context) error {
	if e := s.browserSetupAllowed(ctx); e != nil {
		return e
	}
	if s.openBrowserExtension == nil {
		return browserhost.ErrUnsupported
	}
	s.prepareBrowserHost(ctx)
	return s.openBrowserExtension()
}

func SetBrowserProfileDirectoryPicker(s *AutomationService, choose func(string) (string, error)) {
	s.chooseBrowserDirectory = choose
}
func (s *AutomationService) ConfigureBrowserLaunch(ctx context.Context, id string) (automation.Snapshot, error) {
	if err := s.browserSetupAllowed(ctx); err != nil {
		return automation.Snapshot{}, err
	}
	state := s.service.Snapshot()
	if state.Profiles.Selected != id || s.chooseBrowserDirectory == nil {
		return state, automation.ErrRevoked
	}
	directory := ""
	for _, b := range state.Profiles.Bindings {
		if b.ID == id {
			directory = b.LaunchDirectory
		}
	}
	chosen, err := s.chooseBrowserDirectory(directory)
	if err != nil || chosen == "" {
		return s.service.Snapshot(), err
	}
	err = s.service.ConfigureBrowserLaunch(ctx, id, chosen)
	return s.service.Snapshot(), err
}
func (s *AutomationService) ClearBrowserLaunch(ctx context.Context, id string) (automation.Snapshot, error) {
	if err := s.browserSetupAllowed(ctx); err != nil {
		return automation.Snapshot{}, err
	}
	err := s.service.ConfigureBrowserLaunch(ctx, id, "")
	return s.service.Snapshot(), err
}
func (s *AutomationService) OpenBrowserProfile(ctx context.Context, id string) (automation.Snapshot, error) {
	if err := s.browserSetupAllowed(ctx); err != nil {
		return automation.Snapshot{}, err
	}
	err := s.service.OpenBrowserProfile(ctx, id)
	return s.service.Snapshot(), err
}

func (s *AutomationService) AnswerBrowserConsent(ctx context.Context, id, choice string) (automation.Snapshot, error) {
	if err := s.browserSetupAllowed(ctx); err != nil {
		return automation.Snapshot{}, err
	}
	return s.service.AnswerBrowserConsent(ctx, id, choice)
}
func (s *AutomationService) SetBrowserPolicy(ctx context.Context, revision uint64, policy settings.BrowserPolicy) (automation.Snapshot, error) {
	if err := s.browserSetupAllowed(ctx); err != nil {
		return automation.Snapshot{}, err
	}
	return s.service.SetBrowserPolicy(ctx, revision, policy)
}
func (s *AutomationService) StopBrowserUse(ctx context.Context, session, lifetime string) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work.")
	}
	err := s.service.StopBrowserUse(session, lifetime)
	return s.service.Snapshot(), err
}

func SetBrowserConversationOpener(s *AutomationService, open func(context.Context, string) error) {
	s.openBrowserConversation = open
}
func (s *AutomationService) OpenBrowserConversation(ctx context.Context, session string) error {
	if !isTrustedWindow(ctx, "workspace") {
		return trustedSurfaceRequired("Open DSH Work.")
	}
	if s.openBrowserConversation == nil {
		return automation.ErrUnavailable
	}
	snapshot := s.service.Snapshot()
	if snapshot.BrowserAccess == nil {
		return automation.ErrUnavailable
	}
	for _, entry := range snapshot.BrowserAccess.Sessions {
		if entry.Session == session {
			return s.openBrowserConversation(ctx, session)
		}
	}
	return automation.ErrRevoked
}
