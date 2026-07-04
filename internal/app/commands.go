// Package app implements sshpic's command-line interface.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/doctor"
	"github.com/leekyungmoon/sshpic/internal/paste"
	"github.com/leekyungmoon/sshpic/internal/pathfmt"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

type parsedArgs struct {
	Positionals []string
	Values      map[string]string
	Bools       map[string]bool
}

func Run(args []string, build BuildInfo, stdout, stderr io.Writer) int {
	pa, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(pa.Positionals) == 0 || pa.Bools["help"] {
		usage(stdout)
		return 0
	}
	cmd := pa.Positionals[0]
	if cmd == "help" {
		usage(stdout)
		return 0
	}
	if cmd == "version" {
		fmt.Fprintf(stdout, "sshpic %s commit=%s date=%s\n", firstNonEmpty(build.Version, "0.1.0"), firstNonEmpty(build.Commit, "dev"), firstNonEmpty(build.Date, "unknown"))
		return 0
	}
	ctx := context.Background()
	switch cmd {
	case "init":
		return runInit(pa, stdout, stderr)
	case "snippet":
		return runSnippet(pa, stdout, stderr)
	case "install":
		return runInstall(pa, stdout, stderr)
	case "doctor":
		return runDoctor(pa, stdout, stderr)
	case "paste":
		return runPaste(ctx, pa, stdout, stderr)
	case "iterm2-paste":
		return runITerm2Paste(ctx, pa, stdout, stderr)
	case "iterm2-dispatch":
		return runITerm2Dispatch(ctx, pa, stdout, stderr)
	case "clip", "shot", "full", "file":
		return runUploadCommand(ctx, cmd, pa, stdout, stderr)
	case "clean":
		return runClean(ctx, pa, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

func runInit(pa parsedArgs, stdout, stderr io.Writer) int {
	path := pa.Values["config"]
	if path == "" {
		var err error
		path, err = config.ResolvePath(config.Overrides{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := config.WriteDefault(path, pa.Bools["force"]); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s\n", path)
	return 0
}

func runSnippet(pa parsedArgs, stdout, stderr io.Writer) int {
	if len(pa.Positionals) < 2 || pa.Positionals[1] != "iterm2" {
		fmt.Fprintln(stderr, "usage: sshpic snippet iterm2")
		return 2
	}
	cfg, _, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprint(stdout, iterm2.SnippetFor(cfg).Text)
	return 0
}

func runInstall(pa parsedArgs, stdout, stderr io.Writer) int {
	if len(pa.Positionals) < 2 || pa.Positionals[1] != "iterm2" {
		fmt.Fprintln(stderr, "usage: sshpic install iterm2")
		return 2
	}
	cfg, path, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		fmt.Fprintf(stderr, "cannot determine sshpic executable path: %v\n", err)
		return 1
	}
	exe, _ = filepath.Abs(exe)
	var migrated bool
	if _, explicit := pa.Values["remote_dir"]; !explicit && os.Getenv("SSHPIC_REMOTE_DIR") == "" {
		var migrateErr error
		cfg, migrated, migrateErr = config.MigrateLegacyDefaults(path, cfg)
		if migrateErr != nil {
			fmt.Fprintln(stderr, migrateErr)
			return 1
		}
	}
	result, err := iterm2.Install(context.Background(), cfg, path, iterm2.InstallOptions{
		BinaryPath:   exe,
		RemoteHost:   pa.Values["remote_host"],
		Force:        pa.Bools["force"],
		GlobalKeyMap: true,
		LaunchDaemon: true,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fprintNoExtraBlank(stdout, iterm2.InstallSummary(result))
	if migrated {
		fprintNoExtraBlank(stdout, "config migrated: legacy /tmp remote_dir -> "+cfg.RemoteDir)
	}
	return 0
}

func runDoctor(pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, path, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "config: %s\n", path)
	checks := doctor.Run(cfg)
	for _, check := range checks {
		fmt.Fprintf(stdout, "[%s] %s - %s\n", check.Status, check.Name, check.Detail)
	}
	if doctor.HasFatal(checks) {
		return 1
	}
	return 0
}

func runPaste(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	src := sourceFromConfig(cfg)
	uploader := upload.SSHCat{Host: cfg.RemoteHost}
	res, err := paste.Execute(ctx, cfg, src, uploader, paste.Options{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	output := pa.Values["output"]
	if output == "" {
		output = "text"
	}
	switch output {
	case "payload":
		_, _ = io.WriteString(stdout, res.Payload)
	case "json":
		_ = json.NewEncoder(stdout).Encode(res)
	case "text":
		fmt.Fprintln(stdout, res.Payload)
	default:
		fmt.Fprintf(stderr, "unknown output mode %q\n", output)
		return 2
	}
	return 0
}

func runITerm2Paste(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		appendIntegrationLog("config load failed: " + err.Error())
		return 1
	}
	src := sourceFromConfig(cfg)
	uploader, remoteUser := iterm2Uploader(ctx, cfg, iterm2.SessionContext{
		SessionID:   pa.Values["session_id"],
		TTY:         pa.Values["session_tty"],
		CommandLine: pa.Values["session_command_line"],
		JobPID:      pa.Values["session_job_pid"],
	})
	res, err := paste.Execute(ctx, cfg, src, uploader, paste.Options{RemoteUser: remoteUser})
	if err != nil {
		appendIntegrationLog("paste failed: " + err.Error())
		return 1
	}
	output := pa.Values["output"]
	if output == "" {
		output = "payload"
	}
	switch output {
	case "payload":
		_, _ = io.WriteString(stdout, res.Payload)
	case "json":
		_ = json.NewEncoder(stdout).Encode(res)
	case "text":
		fmt.Fprintln(stdout, res.Payload)
	default:
		appendIntegrationLog("unknown output mode: " + output)
		return 2
	}
	return 0
}

type iterm2DispatchResult struct {
	Action  string `json:"action"`
	Kind    string `json:"kind,omitempty"`
	Payload string `json:"payload,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func runITerm2Dispatch(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		appendIntegrationLog("dispatch config load failed: " + err.Error())
		return 1
	}
	result := buildITerm2Dispatch(ctx, cfg, pa)
	if err := writeDispatchFiles(pa, result); err != nil {
		appendIntegrationLog("dispatch file write failed: " + err.Error())
		return 1
	}
	switch output := firstNonEmpty(pa.Values["output"], "none"); output {
	case "none", "":
		return 0
	case "json":
		_ = json.NewEncoder(stdout).Encode(result)
		return 0
	default:
		appendIntegrationLog("unknown dispatch output mode: " + output)
		return 2
	}
}

func buildITerm2Dispatch(ctx context.Context, cfg config.Config, pa parsedArgs) iterm2DispatchResult {
	return buildITerm2DispatchWithSource(ctx, cfg, pa, sourceFromConfig(cfg))
}

func buildITerm2DispatchWithSource(ctx context.Context, cfg config.Config, pa parsedArgs, src provider.LocalImageSource) iterm2DispatchResult {
	uploader, remoteUser := iterm2Uploader(ctx, cfg, iterm2.SessionContext{
		SessionID:   pa.Values["session_id"],
		TTY:         pa.Values["session_tty"],
		CommandLine: pa.Values["session_command_line"],
		JobPID:      pa.Values["session_job_pid"],
	})
	img, err := src.ReadClipboardImage(ctx)
	if errors.Is(err, provider.ErrNoImage) {
		appendIntegrationLog("dispatch classification: native_paste no_image")
		return iterm2DispatchResult{Action: "native_paste", Kind: "non_image", Reason: "no image clipboard"}
	}
	if err != nil {
		appendIntegrationLog("dispatch classification: native_paste image_read_error: " + err.Error())
		return iterm2DispatchResult{Action: "native_paste", Kind: "unknown", Reason: "image clipboard read failed"}
	}
	res, err := paste.UploadClipboardImage(ctx, cfg, img, src, uploader, paste.Options{Now: time.Now(), RemoteUser: remoteUser})
	if err != nil {
		appendIntegrationLog("dispatch image upload failed: " + err.Error())
		return iterm2DispatchResult{Action: "native_paste", Kind: "image", Reason: "image upload failed"}
	}
	appendIntegrationLog("dispatch classification: insert image payload")
	return iterm2DispatchResult{Action: "insert", Kind: "image", Payload: res.Payload}
}

func writeDispatchFiles(pa parsedArgs, result iterm2DispatchResult) error {
	if path := strings.TrimSpace(pa.Values["action_file"]); path != "" {
		if err := os.WriteFile(path, []byte(result.Action), 0o600); err != nil {
			return err
		}
	}
	if path := strings.TrimSpace(pa.Values["payload_file"]); path != "" {
		if result.Action == "insert" {
			if err := os.WriteFile(path, []byte(result.Payload), 0o600); err != nil {
				return err
			}
		} else {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func iterm2Uploader(ctx context.Context, cfg config.Config, sess iterm2.SessionContext) (upload.SSHCat, string) {
	if target, ok := iterm2.DetectSSHTarget(ctx, sess); ok {
		return upload.SSHCat{Args: target.Args}, target.User
	}
	return upload.SSHCat{Host: cfg.RemoteHost}, ""
}

func runUploadCommand(ctx context.Context, cmd string, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	src := sourceFromConfig(cfg)
	uploader := upload.SSHCat{Host: cfg.RemoteHost}
	var images []provider.LocalImage
	switch cmd {
	case "clip":
		img, err := src.ReadClipboardImage(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		images = append(images, img)
	case "shot":
		img, err := src.CaptureRegion(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		images = append(images, img)
	case "full":
		img, err := src.CaptureFullScreen(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		images = append(images, img)
	case "file":
		paths := pa.Positionals[1:]
		if len(paths) == 0 {
			fmt.Fprintln(stderr, "usage: sshpic file <path...>")
			return 2
		}
		for _, p := range paths {
			img, err := provider.FileImage(p)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			images = append(images, img)
		}
	}
	results := []paste.Result{}
	for _, img := range images {
		res, err := paste.UploadLocal(ctx, cfg, img, src, uploader, time.Now())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		results = append(results, res)
	}
	if pa.Bools["json"] {
		_ = json.NewEncoder(stdout).Encode(results)
		return 0
	}
	for _, res := range results {
		fmt.Fprintln(stdout, res.RemotePath)
		if pa.Bools["debug"] && res.Verify.LocalSHA != "" {
			fmt.Fprintf(stderr, "verified sha256 local=%s remote=%s\n", res.Verify.LocalSHA, res.Verify.RemoteSHA)
		}
		if pa.Bools["debug"] {
			for _, warning := range res.Warnings {
				fmt.Fprintf(stderr, "warning: %s\n", warning)
			}
		}
	}
	return 0
}

func runClean(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "user"
	}
	remoteDir := pathfmt.ExpandRemoteDir(cfg.RemoteDir, user, os.Getenv("HOME"))
	dryRun := pa.Bools["dry-run"] || !pa.Bools["yes"]
	if err := upload.ValidateCleanDir(remoteDir, os.Getenv("HOME")); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if dryRun && !pa.Bools["dry-run"] {
		fmt.Fprintln(stderr, "defaulting to dry-run; pass --yes to delete")
	}
	uploader := upload.SSHCat{Host: cfg.RemoteHost}
	out, err := uploader.Clean(ctx, remoteDir, dryRun)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if out == "" {
		fmt.Fprintln(stdout, "no sshpic files found")
	} else {
		fmt.Fprint(stdout, out)
	}
	return 0
}

func loadConfig(pa parsedArgs) (config.Config, string, error) {
	values := map[string]string{}
	for k, v := range pa.Values {
		if nonConfigValueFlag(k) {
			continue
		}
		values[k] = v
	}
	if pa.Bools["insert-newline"] {
		values["insert_newline"] = "true"
	}
	if pa.Bools["no-copy"] {
		values["copy_to_clipboard"] = "false"
	}
	if pa.Bools["no-verify"] {
		values["verify_sha256"] = "false"
	}
	return config.Load(config.Overrides{ConfigPath: pa.Values["config"], Values: values})
}

func nonConfigValueFlag(key string) bool {
	switch key {
	case "config", "output", "session_id", "session_tty", "session_command_line", "session_job_pid", "action_file", "payload_file":
		return true
	default:
		return false
	}
}

func sourceFromConfig(cfg config.Config) provider.MacOSProvider {
	return provider.MacOSProvider{
		ClipboardTool:     cfg.MacOS.ClipboardTool,
		ScreenshotTool:    cfg.MacOS.ScreenshotTool,
		TextClipboardTool: cfg.MacOS.TextClipboardTool,
		CopyTool:          cfg.MacOS.CopyTool,
	}
}

func parseArgs(args []string) (parsedArgs, error) {
	pa := parsedArgs{Values: map[string]string{}, Bools: map[string]bool{}}
	boolFlags := map[string]bool{"help": true, "debug": true, "json": true, "dry-run": true, "yes": true, "force": true, "no-copy": true, "insert-newline": true, "no-verify": true, "no-open": true}
	valueFlags := map[string]bool{"config": true, "remote-host": true, "remote-dir": true, "copy-to-clipboard": true, "filename-template": true, "output": true, "mode": true, "terminal": true, "shortcut": true, "text-passthrough": true, "macos-clipboard-tool": true, "macos-screenshot-tool": true, "macos-text-clipboard-tool": true, "macos-copy-tool": true, "upload-method": true, "verify-sha256": true, "session-id": true, "session-tty": true, "session-command-line": true, "session-job-pid": true, "action-file": true, "payload-file": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pa.Positionals = append(pa.Positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") || arg == "-" {
			pa.Positionals = append(pa.Positionals, arg)
			continue
		}
		nameValue := strings.TrimPrefix(arg, "--")
		name, value, hasValue := strings.Cut(nameValue, "=")
		if boolFlags[name] {
			if hasValue {
				pa.Bools[name] = value == "true" || value == "1" || value == "yes"
			} else {
				pa.Bools[name] = true
			}
			continue
		}
		if !valueFlags[name] {
			return pa, fmt.Errorf("unknown flag --%s", name)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return pa, fmt.Errorf("flag --%s requires a value", name)
			}
			i++
			value = args[i]
		}
		pa.Values[strings.ReplaceAll(name, "-", "_")] = value
	}
	return pa, nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, `sshpic - paste local screenshots into remote SSH terminal agents

Usage:
  sshpic init [--force]
  sshpic paste [--output=payload|json|text]
  sshpic clip [--debug]
  sshpic shot
  sshpic full
  sshpic file <path...>
  sshpic doctor
  sshpic clean [--dry-run|--yes]
  sshpic version
  sshpic snippet iterm2
  sshpic install iterm2 [--remote-host <host>] [--no-open]

Global flags:
  --config <path>              config path (default ~/.config/sshpic/config.toml)
  --remote-host <host>         SSH host for uploads
  --remote-dir <path>          remote directory, default /home/${USER}/.sshpic/images
  --output=payload             paste primitive: stdout is only insertable payload
  --insert-newline             opt-in newline after payload
  --no-copy                    do not copy remote path back to local clipboard
  --no-verify                  skip remote SHA256 verification
  --no-open                    accepted for compatibility; no-op in v0.1
`)
}

func fprintNoExtraBlank(w io.Writer, text string) {
	_, _ = io.WriteString(w, strings.TrimRight(text, "\n")+"\n")
}

func appendIntegrationLog(message string) {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil || home == "" {
			return
		}
		cacheDir = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(cacheDir, "sshpic")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "sshpic.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(message))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
