//go:build windows

package putty

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows/registry"
)

var managedSessionRegistryTestSequence atomic.Uint64

func TestManagedSessionRegistryCreatesAndReadsBackExactState(t *testing.T) {
	useIsolatedManagedSessionRegistry(t)
	specs := testManagedSessionSpecifications(t)

	if err := provisionManagedSessionsPlatform(specs); err != nil {
		t.Fatal(err)
	}
	if err := verifyManagedSessionsPlatform(specs); err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		key := openManagedSessionTestKey(t, spec.Name, registry.QUERY_VALUE)
		if err := verifyManagedSessionKey(key, spec); err != nil {
			_ = key.Close()
			t.Fatalf("read back %q: %v", spec.Name, err)
		}
		if err := key.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagedSessionRegistryVerificationRejectsTamperingWithoutRepair(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(t *testing.T, key registry.Key)
		check  func(t *testing.T, key registry.Key)
	}{
		{
			name: "changed policy value",
			tamper: func(t *testing.T, key registry.Key) {
				t.Helper()
				if err := key.SetDWordValue("LogType", 1); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, key registry.Key) {
				t.Helper()
				value, valueType, err := key.GetIntegerValue("LogType")
				if err != nil || valueType != registry.DWORD || value != 1 {
					t.Fatalf("runtime verification repaired changed value: value=%d type=%d err=%v", value, valueType, err)
				}
			},
		},
		{
			name: "unexpected value",
			tamper: func(t *testing.T, key registry.Key) {
				t.Helper()
				if err := key.SetStringValue("UnexpectedValue", "preserve-me"); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, key registry.Key) {
				t.Helper()
				value, valueType, err := key.GetStringValue("UnexpectedValue")
				if err != nil || valueType != registry.SZ || value != "preserve-me" {
					t.Fatalf("runtime verification repaired unexpected value: value=%q type=%d err=%v", value, valueType, err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			useIsolatedManagedSessionRegistry(t)
			specs := testManagedSessionSpecifications(t)
			if err := provisionManagedSessionsPlatform(specs); err != nil {
				t.Fatal(err)
			}

			key := openManagedSessionTestKey(t, specs[0].Name, registry.QUERY_VALUE|registry.SET_VALUE)
			test.tamper(t, key)
			if err := key.Close(); err != nil {
				t.Fatal(err)
			}

			if err := verifyManagedSessionsPlatform(specs); err == nil {
				t.Fatal("tampered managed session unexpectedly verified")
			}
			key = openManagedSessionTestKey(t, specs[0].Name, registry.QUERY_VALUE)
			test.check(t, key)
			if err := key.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagedSessionRegistryPreservesUnownedCollision(t *testing.T) {
	useIsolatedManagedSessionRegistry(t)
	specs := testManagedSessionSpecifications(t)

	key, _, err := registry.CreateKey(
		registry.CURRENT_USER,
		puttySessionsRegistryPath+specs[0].Name,
		registry.QUERY_VALUE|registry.SET_VALUE,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := key.SetStringValue("UserSentinel", "keep-me"); err != nil {
		_ = key.Close()
		t.Fatal(err)
	}
	if err := key.Close(); err != nil {
		t.Fatal(err)
	}

	if err := provisionManagedSessionsPlatform(specs); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("provision collision error=%v", err)
	}
	assertManagedSessionTestString(t, specs[0].Name, "UserSentinel", "keep-me")
	assertManagedSessionTestKeyMissing(t, specs[1].Name)

	if err := removeManagedSessionsPlatform(specs); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("remove collision error=%v", err)
	}
	assertManagedSessionTestString(t, specs[0].Name, "UserSentinel", "keep-me")
}

func TestManagedSessionRegistryRepairsOwnedState(t *testing.T) {
	useIsolatedManagedSessionRegistry(t)
	specs := testManagedSessionSpecifications(t)
	if err := provisionManagedSessionsPlatform(specs); err != nil {
		t.Fatal(err)
	}

	key := openManagedSessionTestKey(t, specs[0].Name, registry.QUERY_VALUE|registry.SET_VALUE)
	if err := key.SetDWordValue("LogType", 1); err != nil {
		_ = key.Close()
		t.Fatal(err)
	}
	if err := key.SetStringValue("UnexpectedValue", "remove-me"); err != nil {
		_ = key.Close()
		t.Fatal(err)
	}
	if err := key.Close(); err != nil {
		t.Fatal(err)
	}

	if err := provisionManagedSessionsPlatform(specs); err != nil {
		t.Fatal(err)
	}
	if err := verifyManagedSessionsPlatform(specs); err != nil {
		t.Fatal(err)
	}
	key = openManagedSessionTestKey(t, specs[0].Name, registry.QUERY_VALUE)
	if _, _, err := key.GetStringValue("UnexpectedValue"); !errors.Is(err, registry.ErrNotExist) {
		_ = key.Close()
		t.Fatalf("unexpected value survived owned repair: %v", err)
	}
	if err := key.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedSessionRegistryRemovalDeletesOnlyOwnedSessions(t *testing.T) {
	useIsolatedManagedSessionRegistry(t)
	specs := testManagedSessionSpecifications(t)
	if err := provisionManagedSessionsPlatform(specs); err != nil {
		t.Fatal(err)
	}

	const personalSession = "Personal Session"
	key, _, err := registry.CreateKey(
		registry.CURRENT_USER,
		puttySessionsRegistryPath+personalSession,
		registry.QUERY_VALUE|registry.SET_VALUE,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := key.SetStringValue("UserSentinel", "keep-me"); err != nil {
		_ = key.Close()
		t.Fatal(err)
	}
	if err := key.Close(); err != nil {
		t.Fatal(err)
	}

	if err := removeManagedSessionsPlatform(specs); err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		assertManagedSessionTestKeyMissing(t, spec.Name)
	}
	assertManagedSessionTestString(t, personalSession, "UserSentinel", "keep-me")
}

func useIsolatedManagedSessionRegistry(t *testing.T) {
	t.Helper()
	if puttySessionsRegistryPath != defaultPuttySessionsRegistryPath {
		t.Fatalf("managed session registry test seam already active: %q", puttySessionsRegistryPath)
	}
	testRoot := fmt.Sprintf(
		`Software\sshpic-managed-sessions-test-%d-%d`,
		os.Getpid(),
		managedSessionRegistryTestSequence.Add(1),
	)
	puttySessionsRegistryPath = testRoot + `\Sessions\`
	t.Cleanup(func() {
		puttySessionsRegistryPath = defaultPuttySessionsRegistryPath
		if err := deleteManagedSessionRegistryTestTree(registry.CURRENT_USER, testRoot); err != nil && !errors.Is(err, registry.ErrNotExist) {
			t.Errorf("clean isolated registry subtree: %v", err)
		}
	})
}

func testManagedSessionSpecifications(t *testing.T) []managedSessionSpec {
	t.Helper()
	specs, err := managedSessionSpecifications(`C:\Program Files\PuTTY\plink.exe`)
	if err != nil {
		t.Fatal(err)
	}
	return specs
}

func openManagedSessionTestKey(t *testing.T, name string, access uint32) registry.Key {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, puttySessionsRegistryPath+name, access)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func assertManagedSessionTestString(t *testing.T, session, name, want string) {
	t.Helper()
	key := openManagedSessionTestKey(t, session, registry.QUERY_VALUE)
	got, valueType, err := key.GetStringValue(name)
	closeErr := key.Close()
	if err != nil || valueType != registry.SZ || got != want {
		t.Fatalf("%s/%s=%q type=%d err=%v, want %q", session, name, got, valueType, err, want)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func assertManagedSessionTestKeyMissing(t *testing.T, name string) {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, puttySessionsRegistryPath+name, registry.QUERY_VALUE)
	if err == nil {
		_ = key.Close()
		t.Fatalf("registry key %q still exists", name)
	}
	if !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("open registry key %q: %v", name, err)
	}
}

func deleteManagedSessionRegistryTestTree(root registry.Key, path string) error {
	key, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	subkeys, readErr := key.ReadSubKeyNames(-1)
	closeErr := key.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	for _, subkey := range subkeys {
		if err := deleteManagedSessionRegistryTestTree(root, path+`\`+subkey); err != nil {
			return err
		}
	}
	return registry.DeleteKey(root, path)
}
