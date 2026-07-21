//go:build !windows

package putty

// The PuTTY password-sharing integration is installed only on Windows. This
// no-op keeps its pure argument and uploader tests portable to other builders.
func validateLocalPlinkPath(string) error { return nil }
