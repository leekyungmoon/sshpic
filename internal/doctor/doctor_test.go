package doctor

import (
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/config"
)

func TestRunTargetTerminalappIsProbeOnly(t *testing.T) {
	checks := RunTarget(config.Defaults(), "terminalapp")
	if len(checks) == 0 {
		t.Fatal("expected checks")
	}
	if checks[0].Name != "support_status" || !strings.Contains(checks[0].Detail, "TBD") {
		t.Fatalf("first check=%+v", checks[0])
	}
}

func TestRunTargetUbuntuAlias(t *testing.T) {
	checks := RunTarget(config.Defaults(), "ubuntu")
	if len(checks) == 0 {
		t.Fatal("expected checks")
	}
	if checks[0].Name != "support_status" || !strings.Contains(checks[0].Detail, "TBD") {
		t.Fatalf("first check=%+v", checks[0])
	}
}

func TestRunTargetUnknownFatal(t *testing.T) {
	checks := RunTarget(config.Defaults(), "warp")
	if !HasFatal(checks) {
		t.Fatalf("checks=%+v", checks)
	}
}
