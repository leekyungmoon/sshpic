package putty

import "fmt"

const (
	managedSessionMarkerName     = "SshpicManagedVersion"
	managedSessionMarkerValue    = "sshpic-putty-session-v1"
	managedSessionRoleName       = "SshpicManagedRole"
	managedUpstreamSessionRole   = "password-upstream"
	managedDownstreamSessionRole = "shared-sftp-downstream"
)

type managedSessionSpec struct {
	Name    string
	Role    string
	Strings map[string]string
	DWORDs  map[string]uint32
}

// ProvisionManagedSessions creates or repairs two non-launchable PuTTY saved
// sessions owned by sshpic. They isolate managed Plink processes from the
// user's PuTTY Default Settings. The downstream policy additionally forbids
// becoming a sharing upstream, so upload helpers can only reuse the foreground
// authenticated connection.
func ProvisionManagedSessions(plinkPath string) error {
	specs, err := managedSessionSpecifications(plinkPath)
	if err != nil {
		return err
	}
	if err := provisionManagedSessionsPlatform(specs); err != nil {
		return fmt.Errorf("provision sshpic PuTTY sessions: %w", err)
	}
	return nil
}

// VerifyManagedSessions performs a read-only exact allowlist check immediately
// before Plink starts. Runtime never repairs or partially rewrites registry
// state; an absent or modified managed session fails closed.
func VerifyManagedSessions(plinkPath string) error {
	specs, err := managedSessionSpecifications(plinkPath)
	if err != nil {
		return err
	}
	if err := verifyManagedSessionsPlatform(specs); err != nil {
		return fmt.Errorf("verify sshpic PuTTY sessions (rerun ./install.sh): %w", err)
	}
	return nil
}

// RemoveManagedSessions removes only exact registry sessions carrying
// sshpic's ownership marker. A collision with unrelated user state fails
// closed.
func RemoveManagedSessions() error {
	if err := removeManagedSessionsPlatform([]managedSessionSpec{
		{Name: ManagedUpstreamSessionName, Role: managedUpstreamSessionRole},
		{Name: ManagedDownstreamSessionName, Role: managedDownstreamSessionRole},
	}); err != nil {
		return fmt.Errorf("remove sshpic PuTTY sessions: %w", err)
	}
	return nil
}

func managedSessionSpecifications(plinkPath string) ([]managedSessionSpec, error) {
	proxyCommand, err := denyNetworkProxyCommand(plinkPath)
	if err != nil {
		return nil, err
	}

	baseStrings := map[string]string{
		"HostName":            "",
		"Protocol":            "ssh",
		"LogHost":             "",
		"PreConnectCommand":   "",
		"ProxyExcludeList":    "",
		"ProxyHost":           "",
		"ProxyUsername":       "",
		"ProxyTelnetCommand":  "",
		"UserName":            "",
		"LocalUserName":       "",
		"RemoteCommand":       "",
		"PublicKeyFile":       "",
		"DetachedCertificate": "",
		"AuthPlugin":          "",
		"PortForwardings":     "",
		"SSHManualHostKeys":   "",
		"X11Display":          "",
		"X11AuthFile":         "",
		"Environment":         "",
		"LogFileName":         "",
		"GSSCustom":           "",
	}
	baseDWORDs := map[string]uint32{
		"PortNumber":                  22,
		"Present":                     1,
		"SshProt":                     3,
		"ProxyMethod":                 0,
		"ProxyPort":                   0,
		"ProxyDNS":                    0,
		"ProxyLocalhost":              0,
		"ProxyLogToTerm":              1,
		"ConnectionSharing":           1,
		"ConnectionSharingUpstream":   1,
		"ConnectionSharingDownstream": 1,
		"TryAgent":                    0,
		"AgentFwd":                    0,
		"ChangeUsername":              0,
		"AuthGSSAPI":                  0,
		"AuthGSSAPIKEX":               0,
		"GssapiFwd":                   0,
		"AuthKI":                      1,
		"AuthTIS":                     0,
		"SshNoAuth":                   0,
		"SshNoTrivialAuth":            1,
		"X11Forward":                  0,
		"LocalPortAcceptAll":          0,
		"RemotePortAcceptAll":         0,
		"UserNameFromEnvironment":     0,
		"NoPTY":                       0,
		"SshNoShell":                  0,
		"LogType":                     0,
		"SSHLogOmitPasswords":         1,
		"SSHLogOmitData":              1,
	}

	upstream := managedSessionSpec{
		Name:    ManagedUpstreamSessionName,
		Role:    managedUpstreamSessionRole,
		Strings: cloneStringMap(baseStrings),
		DWORDs:  cloneDWORDMap(baseDWORDs),
	}
	downstream := managedSessionSpec{
		Name:    ManagedDownstreamSessionName,
		Role:    managedDownstreamSessionRole,
		Strings: cloneStringMap(baseStrings),
		DWORDs:  cloneDWORDMap(baseDWORDs),
	}
	downstream.Strings["ProxyTelnetCommand"] = proxyCommand
	downstream.DWORDs["ProxyMethod"] = 5
	downstream.DWORDs["ProxyDNS"] = 2
	downstream.DWORDs["ProxyLocalhost"] = 1
	downstream.DWORDs["ConnectionSharingUpstream"] = 0
	downstream.DWORDs["AuthKI"] = 0
	downstream.DWORDs["NoPTY"] = 1

	return []managedSessionSpec{upstream, downstream}, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for name, value := range source {
		clone[name] = value
	}
	return clone
}

func cloneDWORDMap(source map[string]uint32) map[string]uint32 {
	clone := make(map[string]uint32, len(source))
	for name, value := range source {
		clone[name] = value
	}
	return clone
}
