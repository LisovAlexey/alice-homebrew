package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestArchiveGuards(t *testing.T) {
	for _, n := range []string{"../etc/passwd", "a/../../b", "/etc/passwd", "a\\b", "."} {
		if _, e := cleanArchiveName(n); e == nil {
			t.Fatalf("accepted %q", n)
		}
	}
	for _, pair := range [][2]string{{"a", "/etc/passwd"}, {"a/b", "../../outside"}, {"a/b", "x\\y"}} {
		if safeLink(pair[0], pair[1]) {
			t.Fatalf("accepted link %v", pair)
		}
	}
	if !safeLink("libs/foo", "bar") {
		t.Fatal("valid link rejected")
	}
}
func TestProfileIsolation(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copy")
	_ = os.Mkdir(filepath.Join(src, "logs"), 0700)
	_ = os.WriteFile(filepath.Join(src, "logs", "secret.log"), []byte("log-token"), 0600)
	_ = os.WriteFile(filepath.Join(src, "account"), []byte("recipient-account"), 0600)
	if e := copyProfile(src, dst); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(dst, "logs", "secret.log")); !os.IsNotExist(e) {
		t.Fatal("copied log")
	}
	_ = os.WriteFile(filepath.Join(dst, "account"), []byte("changed"), 0600)
	raw, _ := os.ReadFile(filepath.Join(src, "account"))
	if string(raw) != "recipient-account" {
		t.Fatal("source modified")
	}
	if profileLink("x", "/data/quasar/../../outside") || profileLink("x", "../../outside") {
		t.Fatal("profile escape accepted")
	}
	if !profileLink("a", "/system/vendor/quasar/config.json") {
		t.Fatal("framework link rejected")
	}
}
func TestProfileBudgetAndLink(t *testing.T) {
	src := t.TempDir()
	f, e := os.Create(filepath.Join(src, "large"))
	if e != nil {
		t.Fatal(e)
	}
	_ = f.Truncate(maxProfileBytes + 1)
	f.Close()
	if e = copyProfile(src, t.TempDir()); e == nil {
		t.Fatal("oversized profile accepted")
	}
	src = t.TempDir()
	if e = os.Symlink("/etc/shadow", filepath.Join(src, "escape")); e != nil {
		t.Fatal(e)
	}
	if e = copyProfile(src, t.TempDir()); e == nil {
		t.Fatal("outside symlink accepted")
	}
}
func TestArchiveSymlinkAncestor(t *testing.T) {
	old := spec
	defer func() { spec = old }()
	file := filepath.Join(t.TempDir(), "bad.gz")
	f, _ := os.Create(file)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "safe"})
	_ = tw.WriteHeader(&tar.Header{Name: "link/escape", Mode: 0600, Size: 1})
	_, _ = tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	f.Close()
	raw, _ := os.ReadFile(file)
	h := sha256.Sum256(raw)
	spec.PayloadSHA = hex.EncodeToString(h[:])
	spec.PayloadBytes = int64(len(raw))
	spec.ExpandedBytes = 1
	spec.Entries = 2
	e := unpackPayload(file, t.TempDir())
	if e == nil || !strings.Contains(e.Error(), "предок") {
		t.Fatalf("ancestor not rejected: %v", e)
	}
}
func TestBundleOnlyLocal(t *testing.T) {
	for _, u := range []string{"http://example.com/a", "http://8.8.8.8/a", "file:///etc/passwd", "http://user@192.168.1.2/a", "http://192.168.1.2/a#x"} {
		if _, e := localBundleURL(u); e == nil {
			t.Fatalf("accepted %s", u)
		}
	}
	if _, e := localBundleURL("http://192.168.31.20:18189/token/alice-framework.tar.gz"); e != nil {
		t.Fatal(e)
	}
}
func TestPIDIdentity(t *testing.T) {
	p, e := readProcess(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	if !sameProcess(p) {
		t.Fatal("self mismatch")
	}
	p.Start = "0"
	if sameProcess(p) {
		t.Fatal("reused PID accepted")
	}
}

func TestRuntimePathGuards(t *testing.T) {
	for _, p := range []string{"/", "/tmp", "/data/alice-homebrew/state/plugin-data/alice-legacy-fix", "/tmp/alice-legacy-a/..", "/tmp/alice-legacy-"} {
		if validRuntime(p) {
			t.Fatalf("unsafe runtime %q", p)
		}
	}
	if !validRuntime("/tmp/alice-legacy-123") {
		t.Fatal("valid runtime refused")
	}
}
func TestRealPayload(t *testing.T) {
	archive := os.Getenv("ALICE_TEST_PAYLOAD")
	if archive == "" {
		t.Skip("set ALICE_TEST_PAYLOAD for private donor archive")
	}
	if e := unpackPayload(archive, t.TempDir()); e != nil {
		t.Fatal(e)
	}
}

func TestWatchdogEOFRecovery(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Process.Kill()
	p, e := readProcess(cmd.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	lifeR, lifeW, _ := os.Pipe()
	defer lifeR.Close()
	defer lifeW.Close()
	readyR, readyW, _ := os.Pipe()
	defer readyR.Close()
	defer readyW.Close()
	result := make(chan error, 1)
	restored := make(chan bool, 1)
	go func() {
		result <- watchdogProtocol(lifeR, readyW, p.PID, p.Start, func() error { restored <- true; return nil })
	}()
	_, _ = lifeW.Write([]byte("A"))
	var ack [1]byte
	if _, e = io.ReadFull(readyR, ack[:]); e != nil || ack[0] != 'A' {
		t.Fatal("not armed")
	}
	if !sameProcess(p) {
		t.Fatal("helper killed before controller exit")
	}
	lifeW.Close()
	select {
	case e = <-result:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no recovery after EOF")
	}
	if sameProcess(p) {
		t.Fatal("helper still running at recovery")
	}
	if e = cmd.Wait(); e == nil {
		t.Fatal("helper was not terminated")
	}
	<-restored
}

func TestChrootRecoveryCleanup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root required for mount integration")
	}
	root, e := os.MkdirTemp("/tmp", "alice-legacy-test-")
	if e != nil {
		t.Fatal(e)
	}
	defer cleanupRuntime(root)
	if e = ramMount(root, 4*1024*1024); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"/bin", "/lib", "/lib64", "/usr"} {
		if e = bindHost(root, p, true); e != nil {
			t.Fatal(e)
		}
	}
	cmd := exec.Command("/bin/sleep", "30")
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: root}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Process.Kill()
	if len(rootProcesses(root)) != 1 {
		t.Fatal("chroot process not identified")
	}
	if e = terminateRoot(root); e != nil {
		t.Fatal(e)
	}
	if e = cmd.Wait(); e == nil {
		t.Fatal("chroot process not killed")
	}
	if e = cleanupRuntime(root); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("mount directory not cleaned")
	}
	if _, e = os.Stat("/bin/sleep"); e != nil {
		t.Fatal("host command affected")
	}
}
