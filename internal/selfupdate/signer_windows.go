//go:build windows

package selfupdate

// VerifiedAuthenticodeSigner supplies an application identity only after the
// existing cache-only Windows trust check validates the embedded signer.
func VerifiedAuthenticodeSigner(path string) (string, error) {
	status, e := authenticodeStatus(path)
	if e != nil {
		return "", e
	}
	if status != 0 {
		return "", nil
	}
	return authenticodeSigner(path)
}
