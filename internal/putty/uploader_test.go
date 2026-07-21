package putty

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const testPlinkPath = `C:\Program Files\PuTTY\plink.exe`

func TestSharedUploaderPublishesPrivateVerifiedFile(t *testing.T) {
	localPath := pathForTestFile(t, []byte("clipboard image bytes"))
	fs := newFakeFilesystem("/home/alice")
	shareChecks := 0
	openCalls := 0
	uploader := &SharedUploader{
		PlinkPath:  testPlinkPath,
		Invocation: Invocation{Host: "host", User: "alice", Port: 2222},
		checkShare: func(_ context.Context, executable string, args []string) error {
			shareChecks++
			if executable != testPlinkPath || !contains(args, "-shareexists") {
				t.Fatalf("share check executable=%q args=%q", executable, args)
			}
			return nil
		},
		openFS: func(_ context.Context, executable string, args []string) (remoteFilesystem, error) {
			openCalls++
			if executable != testPlinkPath || !contains(args, "-batch") || !contains(args, "-share") || !contains(args, "sftp") {
				t.Fatalf("open executable=%q args=%q", executable, args)
			}
			return fs, nil
		},
		random: bytes.NewReader(bytes.Repeat([]byte{0x5a}, 32)),
	}
	remotePath := "/home/alice/.sshpic/images/clipboard.png"
	if err := uploader.Upload(context.Background(), localPath, remotePath); err != nil {
		t.Fatal(err)
	}
	if shareChecks != 1 || openCalls != 1 {
		t.Fatalf("shareChecks=%d openCalls=%d", shareChecks, openCalls)
	}
	file := fs.nodes[remotePath]
	if file == nil || string(file.data) != "clipboard image bytes" || file.mode.Perm() != 0o600 || !file.mode.IsRegular() {
		t.Fatalf("published file=%+v", file)
	}
	for _, directory := range []string{"/home/alice/.sshpic", "/home/alice/.sshpic/images"} {
		node := fs.nodes[directory]
		if node == nil || !node.mode.IsDir() || node.mode.Perm() != 0o700 {
			t.Fatalf("directory %s=%+v", directory, node)
		}
	}
	for name := range fs.nodes {
		if path.Base(name) != "clipboard.png" && path.Dir(name) == "/home/alice/.sshpic/images" {
			t.Fatalf("temporary upload was not removed: %s", name)
		}
	}

	result, err := uploader.Verify(context.Background(), localPath, remotePath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Match || result.LocalSHA == "" || result.RemoteSHA != result.LocalSHA {
		t.Fatalf("verify=%+v", result)
	}
	if shareChecks != 2 || openCalls != 2 {
		t.Fatalf("verify did not use a fresh shared channel: shareChecks=%d openCalls=%d", shareChecks, openCalls)
	}
}

func TestSharedUploaderDoesNotOpenChannelWithoutSharingUpstream(t *testing.T) {
	localPath := pathForTestFile(t, []byte("image"))
	opened := false
	uploader := &SharedUploader{
		PlinkPath:  testPlinkPath,
		Invocation: Invocation{Host: "host", User: "alice"},
		checkShare: func(context.Context, string, []string) error {
			return ErrNoSharedConnection
		},
		openFS: func(context.Context, string, []string) (remoteFilesystem, error) {
			opened = true
			return nil, errors.New("must not run")
		},
	}
	err := uploader.Upload(context.Background(), localPath, "/home/alice/.sshpic/images/clipboard.png")
	if !errors.Is(err, ErrNoSharedConnection) {
		t.Fatalf("error=%v", err)
	}
	if opened {
		t.Fatal("SFTP downstream opened after shareexists failure")
	}
}

func TestSharedUploaderRejectsPathsOutsidePrivateRootAndSymlinks(t *testing.T) {
	localPath := pathForTestFile(t, []byte("image"))
	for _, test := range []struct {
		name       string
		remotePath string
		prepare    func(*fakeFilesystem)
	}{
		{name: "outside", remotePath: "/tmp/clipboard.png"},
		{name: "nested", remotePath: "/home/alice/.sshpic/images/nested/clipboard.png"},
		{
			name:       "symlink",
			remotePath: "/home/alice/.sshpic/images/clipboard.png",
			prepare: func(fs *fakeFilesystem) {
				fs.nodes["/home/alice/.sshpic"] = &fakeNode{mode: os.ModeSymlink | 0o777}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := newFakeFilesystem("/home/alice")
			if test.prepare != nil {
				test.prepare(fs)
			}
			uploader := testUploader(fs)
			if err := uploader.Upload(context.Background(), localPath, test.remotePath); err == nil {
				t.Fatal("unsafe upload unexpectedly succeeded")
			}
		})
	}
}

func TestSharedUploaderRemoteMetadata(t *testing.T) {
	fs := newFakeFilesystem("/srv/accounts/alice")
	uploader := testUploader(fs)
	home, err := uploader.RemoteHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if home != "/srv/accounts/alice" || uploader.User() != "alice" {
		t.Fatalf("home=%q user=%q", home, uploader.User())
	}
}

func testUploader(fs remoteFilesystem) *SharedUploader {
	return &SharedUploader{
		PlinkPath:  testPlinkPath,
		Invocation: Invocation{Host: "host", User: "alice"},
		checkShare: func(context.Context, string, []string) error {
			return nil
		},
		openFS: func(context.Context, string, []string) (remoteFilesystem, error) {
			return fs, nil
		},
		random: bytes.NewReader(bytes.Repeat([]byte{0x23}, 32)),
	}
}

func pathForTestFile(t *testing.T, data []byte) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type fakeFilesystem struct {
	home   string
	nodes  map[string]*fakeNode
	closed bool
}

type fakeNode struct {
	mode os.FileMode
	data []byte
}

func newFakeFilesystem(home string) *fakeFilesystem {
	return &fakeFilesystem{home: home, nodes: map[string]*fakeNode{}}
}

func (fs *fakeFilesystem) Getwd() (string, error) { return fs.home, nil }
func (fs *fakeFilesystem) Lstat(name string) (os.FileInfo, error) {
	node := fs.nodes[name]
	if node == nil {
		return nil, os.ErrNotExist
	}
	return fakeInfo{name: path.Base(name), node: node}, nil
}
func (fs *fakeFilesystem) Mkdir(name string) error {
	if fs.nodes[name] != nil {
		return os.ErrExist
	}
	fs.nodes[name] = &fakeNode{mode: os.ModeDir | 0o755}
	return nil
}
func (fs *fakeFilesystem) Chmod(name string, mode os.FileMode) error {
	node := fs.nodes[name]
	if node == nil {
		return os.ErrNotExist
	}
	node.mode = node.mode.Type() | mode.Perm()
	return nil
}
func (fs *fakeFilesystem) CreateExclusive(name string) (io.WriteCloser, error) {
	if fs.nodes[name] != nil {
		return nil, os.ErrExist
	}
	return &fakeRemoteWriter{close: func(data []byte) {
		fs.nodes[name] = &fakeNode{mode: 0o666, data: append([]byte{}, data...)}
	}}, nil
}
func (fs *fakeFilesystem) OpenRead(name string) (io.ReadCloser, error) {
	node := fs.nodes[name]
	if node == nil {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(node.data)), nil
}
func (fs *fakeFilesystem) PosixRename(oldname, newname string) error {
	node := fs.nodes[oldname]
	if node == nil {
		return os.ErrNotExist
	}
	fs.nodes[newname] = node
	delete(fs.nodes, oldname)
	return nil
}
func (fs *fakeFilesystem) Remove(name string) error {
	delete(fs.nodes, name)
	return nil
}
func (fs *fakeFilesystem) Close() error {
	fs.closed = true
	return nil
}

type fakeRemoteWriter struct {
	bytes.Buffer
	close func([]byte)
}

func (writer *fakeRemoteWriter) Close() error {
	writer.close(writer.Bytes())
	return nil
}

type fakeInfo struct {
	name string
	node *fakeNode
}

func (info fakeInfo) Name() string       { return info.name }
func (info fakeInfo) Size() int64        { return int64(len(info.node.data)) }
func (info fakeInfo) Mode() os.FileMode  { return info.node.mode }
func (info fakeInfo) ModTime() time.Time { return time.Time{} }
func (info fakeInfo) IsDir() bool        { return info.node.mode.IsDir() }
func (info fakeInfo) Sys() any           { return nil }

func TestConnectionArgsDoNotLeakDiagnosticsIntoSFTP(t *testing.T) {
	inv := Invocation{Host: "host", User: "alice", Verbose: 3}
	args, err := BuildSharedSFTPArgs(inv, `C:\Program Files\PuTTY\plink.exe`)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"-v", "-q", "-pw", "-pwfile"} {
		if contains(args, forbidden) {
			t.Fatalf("shared SFTP args leaked %s: %q", forbidden, args)
		}
	}
	wantTail := []string{"-l", "alice", "host", "sftp"}
	if !reflect.DeepEqual(args[len(args)-len(wantTail):], wantTail) {
		t.Fatalf("args tail=%q want %q", args, wantTail)
	}
}
