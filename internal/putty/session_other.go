//go:build !windows

package putty

import "errors"

func provisionManagedSessionsPlatform([]managedSessionSpec) error {
	return errors.New("managed password SSH sessions require Windows PuTTY")
}

func verifyManagedSessionsPlatform([]managedSessionSpec) error {
	return errors.New("managed password SSH sessions require Windows PuTTY")
}

func removeManagedSessionsPlatform([]managedSessionSpec) error { return nil }
