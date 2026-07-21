package putty

import "testing"

func TestManagedSessionSpecificationsSeparateUpstreamAndDownstreamAuthority(t *testing.T) {
	specs, err := managedSessionSpecifications(`C:\Program Files\PuTTY\plink.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("spec count=%d", len(specs))
	}
	upstream, downstream := specs[0], specs[1]
	if upstream.Name != ManagedUpstreamSessionName || downstream.Name != ManagedDownstreamSessionName {
		t.Fatalf("unexpected names: %q %q", upstream.Name, downstream.Name)
	}
	if upstream.Strings["HostName"] != "" || downstream.Strings["HostName"] != "" {
		t.Fatal("managed sessions must remain non-launchable")
	}
	if upstream.DWORDs["LogType"] != 0 || downstream.DWORDs["LogType"] != 0 {
		t.Fatal("managed sessions must disable PuTTY logging")
	}
	if _, stored := upstream.Strings["ProxyPassword"]; stored {
		t.Fatal("managed sessions must not create even an empty proxy-password value")
	}
	if upstream.DWORDs["Present"] != 1 || upstream.DWORDs["SshProt"] != 3 || upstream.DWORDs["ProxyPort"] != 0 {
		t.Fatal("managed upstream is missing protocol or proxy fail-safe values")
	}
	if upstream.DWORDs["TryAgent"] != 0 || upstream.DWORDs["AuthGSSAPI"] != 0 ||
		downstream.DWORDs["TryAgent"] != 0 || downstream.DWORDs["AuthGSSAPI"] != 0 {
		t.Fatal("managed sessions must disable implicit agent and GSSAPI authentication")
	}
	if upstream.DWORDs["ConnectionSharingUpstream"] != 1 {
		t.Fatal("interactive session must be allowed to create the sharing upstream")
	}
	if downstream.DWORDs["ConnectionSharingUpstream"] != 0 || downstream.DWORDs["ConnectionSharingDownstream"] != 1 {
		t.Fatal("SFTP helper must be downstream-only")
	}
	if upstream.DWORDs["AuthKI"] != 1 || downstream.DWORDs["AuthKI"] != 0 {
		t.Fatal("only the foreground session may use keyboard-interactive authentication")
	}
	if downstream.DWORDs["ProxyMethod"] != 5 || downstream.DWORDs["ProxyLocalhost"] != 1 ||
		downstream.DWORDs["ProxyDNS"] != 2 || downstream.Strings["ProxyTelnetCommand"] == "" {
		t.Fatal("downstream local proxy guard is incomplete")
	}
	for _, spec := range specs {
		for _, field := range []string{"RemoteCommand", "PublicKeyFile", "AuthPlugin", "PortForwardings", "SSHManualHostKeys"} {
			if spec.Strings[field] != "" {
				t.Fatalf("%s contains unsafe saved value %s", spec.Name, field)
			}
		}
	}
}
