package dshadapter

// AppProfileDefaultPatch exposes the same defaults used to initialize and
// maintain the managed profile, so observations can exclude shell-owned edits.
func (a *Adapter) AppProfileDefaultPatch() string {
	return appProfilePatchTemplate
}
