package wezterm

import (
	"strings"
	"testing"
	"time"
)

func TestLuaIntegrationSourceUsesFocusedAsyncNativePasteContract(t *testing.T) {
	path := `C:\Users\김 사용자\sshpic\sshpic.exe`
	source, err := LuaIntegrationSource(LuaOptions{
		BinaryPath: path, PollInterval: 50 * time.Millisecond, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		luaOwnerMarker,
		`local sshpic_binary = "C:\\Users\\김 사용자\\sshpic\\sshpic.exe"`,
		"key = 'v'", "mods = 'CTRL'", "wezterm.action_callback",
		"pane:get_foreground_process_info()", "info.executable", "info.argv",
		"executable ~= 'ssh' and executable ~= 'ssh.exe'",
		"wezterm.json_encode", "wezterm.background_child_process", "local args = {",
		"'--process-json', process_json", "'--result-file', result_path",
		"wezterm.time.call_after", "in_flight[pane_id]", "win:active_pane()",
		"module_nonce",
		"original_process_is_current", "same_argv(current.argv, original.argv)",
		"normalize_executable(current.executable)", "delayed_target_is_current",
		"forbidden_path_bytes", "safe_remote_path(result.payload)", "unsafe_remote_path",
		"pane:send_paste(result.payload)", "wezterm.action.PasteFrom 'Clipboard'",
		"result.reason", "toast_notification", "os.remove(result_path)",
		"focused_process_diagnostic", "process_info_unavailable", "process_info_unusable",
		"WezTerm reported ssh/ssh.exe without usable argv", "SSH image paste was not attempted",
		"helper_start_error", "sshpic helper could not start",
		"ipairs({ 'TMP', 'TEMP', 'USERPROFILE', 'WINDIR' })",
		"type(candidate) == 'string' and candidate ~= ''",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("Lua source missing %q", want)
		}
	}
	for _, forbidden := range []string{"ReadClipboardText", "get_clipboard", "copy_to_clipboard", "send_text(result.payload)", "shell_split"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("Lua source must not contain %q", forbidden)
		}
	}
	if strings.Count(source, "wezterm.background_child_process") != 1 {
		t.Fatalf("expected one argv-list background dispatch")
	}
	if strings.Count(source, "pane:get_foreground_process_info()") < 2 {
		t.Fatal("delayed completion must re-query focused process identity")
	}
	tmp := strings.Index(source, "ipairs({ 'TMP', 'TEMP', 'USERPROFILE', 'WINDIR' })")
	resultPath := strings.Index(source, "local function temp_result_path")
	if resultPath < 0 || tmp < resultPath {
		t.Fatal("result-file directory must reproduce Windows os.TempDir precedence inside temp_result_path")
	}
	nonSSH := strings.Index(source, "if executable ~= 'ssh' and executable ~= 'ssh.exe' then\n    return nil")
	missingArgv := strings.Index(source, "WezTerm reported ssh/ssh.exe without usable argv")
	if nonSSH < 0 || missingArgv < 0 || nonSSH > missingArgv {
		t.Fatal("non-SSH panes must select silent native paste before SSH argv diagnostics")
	}
}

func TestLuaIntegrationSourceValidatesTimingAndBinary(t *testing.T) {
	for _, opts := range []LuaOptions{
		{},
		{BinaryPath: "sshpic.exe", PollInterval: time.Millisecond},
		{BinaryPath: "sshpic.exe", Timeout: 500 * time.Millisecond},
	} {
		if _, err := LuaIntegrationSource(opts); err == nil {
			t.Fatalf("expected invalid options error: %+v", opts)
		}
	}
}

func TestConfigBlockHasExactOwnedMarkers(t *testing.T) {
	block := configBlock(`C:\Users\alice\.config\wezterm\sshpic-wezterm.lua`, "config")
	if strings.Count(block, configBegin) != 1 || strings.Count(block, configEnd) != 1 {
		t.Fatalf("block=%s", block)
	}
	if !strings.Contains(block, `.apply_to_config(config)`) || !strings.Contains(block, `dofile("C:\\Users\\alice`) {
		t.Fatalf("block=%s", block)
	}
}
