package putty

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"unicode"

	"github.com/leekyungmoon/sshpic/internal/upload"
	"github.com/pkg/sftp"
)

const maxPlinkDiagnosticBytes = 8 << 10

type shareChecker func(context.Context, string, []string) error
type filesystemOpener func(context.Context, string, []string) (remoteFilesystem, error)

// SharedUploader implements paste.RemoteUploader by opening SFTP subsystem
// channels through an already authenticated PuTTY sharing upstream. It never
// accepts, stores, or forwards a password.
type SharedUploader struct {
	PlinkPath  string
	Invocation Invocation

	checkShare shareChecker
	openFS     filesystemOpener
	random     io.Reader
}

// NewSharedUploader validates the invocation and resolves the Plink executable
// up front so paste-time failures cannot silently select a different program.
func NewSharedUploader(plinkPath string, inv Invocation) (*SharedUploader, error) {
	if err := validateInvocation(inv); err != nil {
		return nil, err
	}
	resolved, err := ResolvePlink(plinkPath)
	if err != nil {
		return nil, err
	}
	if err := VerifyManagedSessions(resolved); err != nil {
		return nil, err
	}
	if err := verifyRuntimePlinkVersion(context.Background(), resolved); err != nil {
		return nil, err
	}
	return &SharedUploader{PlinkPath: resolved, Invocation: inv}, nil
}

// User returns the explicit remote account, if one was present in the focused
// invocation. PuTTY may still supply a saved-session user when this is empty.
func (u *SharedUploader) User() string { return u.Invocation.User }

// CheckShared proves that a viable PuTTY upstream exists. Plink's
// -shareexists operation does not initiate a network connection.
func (u *SharedUploader) CheckShared(ctx context.Context) error {
	plinkPath, err := u.resolvedPlink()
	if err != nil {
		return err
	}
	if u.checkShare == nil && u.openFS == nil {
		if err := VerifyManagedSessions(plinkPath); err != nil {
			return err
		}
		if err := verifyRuntimePlinkVersion(ctx, plinkPath); err != nil {
			return err
		}
	}
	args, err := BuildShareExistsArgs(u.Invocation)
	if err != nil {
		return err
	}
	checker := u.checkShare
	if checker == nil {
		checker = checkShareProcess
	}
	if err := checker(ctx, plinkPath, args); err != nil {
		if errors.Is(err, ErrNoSharedConnection) {
			return err
		}
		return fmt.Errorf("check PuTTY sharing upstream: %w", err)
	}
	return nil
}

// RemoteHome returns the SFTP session's canonical starting directory, which is
// the authenticated account home for the supported Ubuntu/POSIX contract.
func (u *SharedUploader) RemoteHome(ctx context.Context) (home string, returnErr error) {
	fs, err := u.openSharedFS(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := fs.Close(); returnErr == nil && err != nil {
			returnErr = err
		}
	}()
	return filesystemHome(fs)
}

// Upload writes through a private temporary file, verifies the bytes by
// reading them back over SFTP, and atomically replaces the requested path.
// Version one deliberately permits only <remote-home>/.sshpic/images/<file>.
func (u *SharedUploader) Upload(ctx context.Context, localPath, remotePath string) (returnErr error) {
	local, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer local.Close()

	fs, err := u.openSharedFS(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := fs.Close(); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	home, err := filesystemHome(fs)
	if err != nil {
		return err
	}
	root := path.Join(home, ".sshpic", "images")
	if err := validateUploadPath(root, remotePath); err != nil {
		return err
	}
	if err := ensurePrivateDir(fs, path.Join(home, ".sshpic")); err != nil {
		return err
	}
	if err := ensurePrivateDir(fs, root); err != nil {
		return err
	}

	suffix, err := randomHex(u.randomReader(), 16)
	if err != nil {
		return fmt.Errorf("create upload nonce: %w", err)
	}
	temporary := path.Join(root, ".sshpic-upload-"+suffix+".tmp")
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = fs.Remove(temporary)
		}
	}()

	remote, err := fs.CreateExclusive(temporary)
	if err != nil {
		return fmt.Errorf("create private remote upload: %w", err)
	}
	localHash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(remote, localHash), local)
	closeErr := remote.Close()
	if copyErr != nil {
		return fmt.Errorf("write remote upload: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close remote upload: %w", closeErr)
	}
	if err := fs.Chmod(temporary, 0o600); err != nil {
		return fmt.Errorf("set private remote upload mode: %w", err)
	}

	remoteHash, err := hashRemote(fs, temporary)
	if err != nil {
		return fmt.Errorf("verify temporary remote upload: %w", err)
	}
	wantHash := hex.EncodeToString(localHash.Sum(nil))
	if remoteHash != wantHash {
		return errors.New("temporary remote upload SHA-256 mismatch")
	}
	if err := fs.PosixRename(temporary, remotePath); err != nil {
		return fmt.Errorf("atomically publish remote upload: %w", err)
	}
	removeTemporary = false
	if err := fs.Chmod(remotePath, 0o600); err != nil {
		return fmt.Errorf("set published remote upload mode: %w", err)
	}
	info, err := fs.Lstat(remotePath)
	if err != nil {
		return fmt.Errorf("inspect published remote upload: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("published remote upload is not a private regular file")
	}
	return nil
}

// Verify independently hashes the local and published remote files. It opens a
// fresh SFTP channel on the same authenticated upstream and never reuses a
// password or initiates an interactive prompt.
func (u *SharedUploader) Verify(ctx context.Context, localPath, remotePath string) (result upload.VerifyResult, returnErr error) {
	localHash, err := upload.FileSHA256(localPath)
	if err != nil {
		return upload.VerifyResult{}, err
	}
	fs, err := u.openSharedFS(ctx)
	if err != nil {
		return upload.VerifyResult{LocalSHA: localHash}, err
	}
	defer func() {
		if err := fs.Close(); returnErr == nil && err != nil {
			returnErr = err
		}
	}()
	home, err := filesystemHome(fs)
	if err != nil {
		return upload.VerifyResult{LocalSHA: localHash}, err
	}
	if err := validateUploadPath(path.Join(home, ".sshpic", "images"), remotePath); err != nil {
		return upload.VerifyResult{LocalSHA: localHash}, err
	}
	remoteHash, err := hashRemote(fs, remotePath)
	if err != nil {
		return upload.VerifyResult{LocalSHA: localHash}, err
	}
	result = upload.VerifyResult{LocalSHA: localHash, RemoteSHA: remoteHash, Match: localHash == remoteHash}
	if !result.Match {
		return result, errors.New("remote upload SHA-256 mismatch")
	}
	return result, nil
}

func (u *SharedUploader) openSharedFS(ctx context.Context) (remoteFilesystem, error) {
	if err := u.CheckShared(ctx); err != nil {
		return nil, err
	}
	plinkPath, err := u.resolvedPlink()
	if err != nil {
		return nil, err
	}
	if u.checkShare == nil && u.openFS == nil {
		if err := VerifyManagedSessions(plinkPath); err != nil {
			return nil, err
		}
		if err := verifyRuntimePlinkVersion(ctx, plinkPath); err != nil {
			return nil, err
		}
	}
	args, err := BuildSharedSFTPArgs(u.Invocation, plinkPath)
	if err != nil {
		return nil, err
	}
	opener := u.openFS
	if opener == nil {
		opener = openSFTPProcess
	}
	return opener(ctx, plinkPath, args)
}

func (u *SharedUploader) resolvedPlink() (string, error) {
	if strings.TrimSpace(u.PlinkPath) == "" {
		return ResolvePlink("")
	}
	if u.checkShare != nil || u.openFS != nil {
		// Test seams may use an inert path, but production constructors resolve
		// and validate the executable before storing it.
		return u.PlinkPath, nil
	}
	return ResolvePlink(u.PlinkPath)
}

func (u *SharedUploader) randomReader() io.Reader {
	if u.random != nil {
		return u.random
	}
	return rand.Reader
}

func checkShareProcess(ctx context.Context, plinkPath string, args []string) error {
	var diagnostic boundedBuffer
	diagnostic.limit = maxPlinkDiagnosticBytes
	cmd := exec.CommandContext(ctx, plinkPath, args...)
	cmd.Stdout = &diagnostic
	cmd.Stderr = &diagnostic
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrNoSharedConnection
	}
	return nil
}

func openSFTPProcess(ctx context.Context, plinkPath string, args []string) (remoteFilesystem, error) {
	cmd := exec.CommandContext(ctx, plinkPath, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Plink SFTP stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open Plink SFTP stdout: %w", err)
	}
	diagnostic := &boundedBuffer{limit: maxPlinkDiagnosticBytes}
	cmd.Stderr = diagnostic
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start shared Plink SFTP channel: %w", err)
	}
	client, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("initialize shared SFTP subsystem: %w", err)
	}
	return &processFilesystem{client: client, stdin: stdin, cmd: cmd, diagnostic: diagnostic}, nil
}

type remoteFilesystem interface {
	Getwd() (string, error)
	Lstat(string) (os.FileInfo, error)
	Mkdir(string) error
	Chmod(string, os.FileMode) error
	CreateExclusive(string) (io.WriteCloser, error)
	OpenRead(string) (io.ReadCloser, error)
	PosixRename(string, string) error
	Remove(string) error
	Close() error
}

type processFilesystem struct {
	client     *sftp.Client
	stdin      io.WriteCloser
	cmd        *exec.Cmd
	diagnostic *boundedBuffer
	closed     bool
}

func (fs *processFilesystem) Getwd() (string, error)                 { return fs.client.Getwd() }
func (fs *processFilesystem) Lstat(name string) (os.FileInfo, error) { return fs.client.Lstat(name) }
func (fs *processFilesystem) Mkdir(name string) error                { return fs.client.Mkdir(name) }
func (fs *processFilesystem) Chmod(name string, mode os.FileMode) error {
	return fs.client.Chmod(name, mode)
}
func (fs *processFilesystem) CreateExclusive(name string) (io.WriteCloser, error) {
	return fs.client.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
}
func (fs *processFilesystem) OpenRead(name string) (io.ReadCloser, error) {
	return fs.client.Open(name)
}
func (fs *processFilesystem) PosixRename(oldname, newname string) error {
	return fs.client.PosixRename(oldname, newname)
}
func (fs *processFilesystem) Remove(name string) error { return fs.client.Remove(name) }
func (fs *processFilesystem) Close() error {
	if fs.closed {
		return nil
	}
	fs.closed = true
	clientErr := fs.client.Close()
	_ = fs.stdin.Close()
	waitErr := fs.cmd.Wait()
	if clientErr != nil {
		return clientErr
	}
	if waitErr != nil {
		return fmt.Errorf("shared Plink SFTP channel exited unsuccessfully")
	}
	return nil
}

func filesystemHome(fs remoteFilesystem) (string, error) {
	home, err := fs.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve remote home through SFTP: %w", err)
	}
	if err := validatePOSIXPath(home); err != nil {
		return "", fmt.Errorf("unsafe SFTP home directory: %w", err)
	}
	return home, nil
}

func ensurePrivateDir(fs remoteFilesystem, name string) error {
	info, err := fs.Lstat(name)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("inspect remote directory: %w", err)
		}
		if err := fs.Mkdir(name); err != nil {
			return fmt.Errorf("create remote directory: %w", err)
		}
		info, err = fs.Lstat(name)
		if err != nil {
			return fmt.Errorf("inspect created remote directory: %w", err)
		}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("remote sshpic path is not a real directory")
	}
	if err := fs.Chmod(name, 0o700); err != nil {
		return fmt.Errorf("set private remote directory mode: %w", err)
	}
	info, err = fs.Lstat(name)
	if err != nil {
		return fmt.Errorf("verify remote directory mode: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("remote sshpic directory is not private")
	}
	return nil
}

func validateUploadPath(root, remotePath string) error {
	if err := validatePOSIXPath(root); err != nil {
		return fmt.Errorf("invalid remote sshpic root: %w", err)
	}
	if err := validatePOSIXPath(remotePath); err != nil {
		return fmt.Errorf("invalid remote upload path: %w", err)
	}
	if path.Dir(remotePath) != root || path.Base(remotePath) == "." || path.Base(remotePath) == ".." {
		return errors.New("remote upload path must be directly inside the private sshpic image directory")
	}
	return nil
}

func validatePOSIXPath(value string) error {
	if value == "" || !strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return errors.New("path is not a clean absolute POSIX path")
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("path contains control or whitespace characters")
		}
	}
	return nil
}

func hashRemote(fs remoteFilesystem, name string) (string, error) {
	file, err := fs.OpenRead(name)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	closeErr := file.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func randomHex(reader io.Reader, bytesCount int) (string, error) {
	data := make([]byte, bytesCount)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	written := len(data)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.buffer.Write(data)
	}
	return written, nil
}
