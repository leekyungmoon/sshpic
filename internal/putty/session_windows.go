//go:build windows

package putty

import (
	"errors"
	"fmt"
	"sort"

	"golang.org/x/sys/windows/registry"
)

const defaultPuttySessionsRegistryPath = `Software\SimonTatham\PuTTY\Sessions\`

// puttySessionsRegistryPath is a package-private test seam. Production code
// never changes it; Windows lifecycle tests replace it with an isolated HKCU
// subtree so they cannot touch the user's real PuTTY sessions.
var puttySessionsRegistryPath = defaultPuttySessionsRegistryPath

func provisionManagedSessionsPlatform(specs []managedSessionSpec) error {
	for _, spec := range specs {
		if err := preflightManagedSession(spec); err != nil {
			return err
		}
	}
	for _, spec := range specs {
		matches, err := managedSessionMatches(spec)
		if err != nil {
			return err
		}
		if matches {
			continue
		}
		if err := writeManagedSession(spec); err != nil {
			return err
		}
	}
	return verifyManagedSessionsPlatform(specs)
}

func verifyManagedSessionsPlatform(specs []managedSessionSpec) error {
	for _, spec := range specs {
		key, err := registry.OpenKey(registry.CURRENT_USER, puttySessionsRegistryPath+spec.Name, registry.QUERY_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("managed PuTTY session %q is missing", spec.Name)
		}
		if err != nil {
			return fmt.Errorf("open managed PuTTY session %q: %w", spec.Name, err)
		}
		verifyErr := verifyManagedSessionKey(key, spec)
		closeErr := key.Close()
		if verifyErr != nil {
			return fmt.Errorf("managed PuTTY session %q changed: %w", spec.Name, verifyErr)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func removeManagedSessionsPlatform(specs []managedSessionSpec) error {
	for _, spec := range specs {
		exists, owned, err := inspectManagedSessionOwnership(spec)
		if err != nil {
			return err
		}
		if exists && !owned {
			return fmt.Errorf("PuTTY session name collision for %q", spec.Name)
		}
	}
	for _, spec := range specs {
		exists, owned, err := inspectManagedSessionOwnership(spec)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if !owned {
			return fmt.Errorf("PuTTY session name collision for %q", spec.Name)
		}
		err = registry.DeleteKey(registry.CURRENT_USER, puttySessionsRegistryPath+spec.Name)
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("delete PuTTY session %q: %w", spec.Name, err)
		}
	}
	return nil
}

func preflightManagedSession(spec managedSessionSpec) error {
	exists, owned, err := inspectManagedSessionOwnership(spec)
	if err != nil {
		return err
	}
	if exists && !owned {
		return fmt.Errorf("PuTTY session name collision for %q", spec.Name)
	}
	return nil
}

func inspectManagedSessionOwnership(spec managedSessionSpec) (exists, owned bool, returnErr error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, puttySessionsRegistryPath+spec.Name, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("open PuTTY session %q: %w", spec.Name, err)
	}
	defer func() {
		if err := key.Close(); returnErr == nil && err != nil {
			returnErr = err
		}
	}()
	marker, markerType, markerErr := key.GetStringValue(managedSessionMarkerName)
	role, roleType, roleErr := key.GetStringValue(managedSessionRoleName)
	owned = markerErr == nil && roleErr == nil && markerType == registry.SZ && roleType == registry.SZ &&
		marker == managedSessionMarkerValue && role == spec.Role
	return true, owned, nil
}

func managedSessionMatches(spec managedSessionSpec) (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, puttySessionsRegistryPath+spec.Name, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open PuTTY session %q: %w", spec.Name, err)
	}
	verifyErr := verifyManagedSessionKey(key, spec)
	closeErr := key.Close()
	if closeErr != nil {
		return false, closeErr
	}
	return verifyErr == nil, nil
}

func writeManagedSession(spec managedSessionSpec) error {
	keyPath := puttySessionsRegistryPath + spec.Name
	exists, owned, err := inspectManagedSessionOwnership(spec)
	if err != nil {
		return err
	}
	if exists {
		if !owned {
			return fmt.Errorf("PuTTY session name collision for %q", spec.Name)
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, keyPath); err != nil {
			return fmt.Errorf("replace PuTTY session %q: %w", spec.Name, err)
		}
	}
	key, openedExisting, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create PuTTY session %q: %w", spec.Name, err)
	}
	if openedExisting {
		_ = key.Close()
		return fmt.Errorf("PuTTY session %q appeared concurrently; rerun installation", spec.Name)
	}
	if err := key.SetStringValue(managedSessionRoleName, spec.Role); err != nil {
		_ = key.Close()
		return fmt.Errorf("mark PuTTY session role %q: %w", spec.Name, err)
	}
	if err := key.SetStringValue(managedSessionMarkerName, managedSessionMarkerValue); err != nil {
		_ = key.Close()
		return fmt.Errorf("mark PuTTY session %q: %w", spec.Name, err)
	}
	for name, value := range spec.Strings {
		if err := key.SetStringValue(name, value); err != nil {
			_ = key.Close()
			return fmt.Errorf("write PuTTY session %q string %q: %w", spec.Name, name, err)
		}
	}
	for name, value := range spec.DWORDs {
		if err := key.SetDWordValue(name, value); err != nil {
			_ = key.Close()
			return fmt.Errorf("write PuTTY session %q DWORD %q: %w", spec.Name, name, err)
		}
	}
	if err := verifyManagedSessionKey(key, spec); err != nil {
		_ = key.Close()
		return fmt.Errorf("verify PuTTY session %q: %w", spec.Name, err)
	}
	if err := key.Close(); err != nil {
		return err
	}
	return nil
}

func verifyManagedSessionKey(key registry.Key, spec managedSessionSpec) error {
	info, err := key.Stat()
	if err != nil {
		return err
	}
	if info.SubKeyCount != 0 {
		return errors.New("managed session contains unexpected subkeys")
	}
	wantNames := make([]string, 0, 2+len(spec.Strings)+len(spec.DWORDs))
	wantNames = append(wantNames, managedSessionMarkerName, managedSessionRoleName)
	for name := range spec.Strings {
		wantNames = append(wantNames, name)
	}
	for name := range spec.DWORDs {
		wantNames = append(wantNames, name)
	}
	sort.Strings(wantNames)
	gotNames, err := key.ReadValueNames(-1)
	if err != nil {
		return err
	}
	sort.Strings(gotNames)
	if len(gotNames) != len(wantNames) {
		return errors.New("registry value allowlist length mismatch")
	}
	for index := range wantNames {
		if gotNames[index] != wantNames[index] {
			return errors.New("registry value allowlist mismatch")
		}
	}
	marker, markerType, err := key.GetStringValue(managedSessionMarkerName)
	if err != nil || markerType != registry.SZ || marker != managedSessionMarkerValue {
		return errors.New("ownership marker mismatch")
	}
	role, roleType, err := key.GetStringValue(managedSessionRoleName)
	if err != nil || roleType != registry.SZ || role != spec.Role {
		return errors.New("role marker mismatch")
	}
	for name, want := range spec.Strings {
		got, valueType, err := key.GetStringValue(name)
		if err != nil || valueType != registry.SZ || got != want {
			return fmt.Errorf("string value %q mismatch", name)
		}
	}
	for name, want := range spec.DWORDs {
		got, valueType, err := key.GetIntegerValue(name)
		if err != nil || valueType != registry.DWORD || got != uint64(want) {
			return fmt.Errorf("DWORD value %q mismatch", name)
		}
	}
	return nil
}
