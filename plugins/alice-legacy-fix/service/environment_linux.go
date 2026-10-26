package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Stock kernel: CONFIG_NAMESPACES is disabled. A chroot on a separate tmpfs
// supplies absolute firmware paths without mounting over the host firmware.
// Never put this tree below plugin-data: manager deletion walks that directory.
func validRuntime(dir string) bool {
	return filepath.Dir(dir) == "/tmp" && strings.HasPrefix(filepath.Base(dir), "alice-legacy-") && len(filepath.Base(dir)) > len("alice-legacy-")
}
func ramMount(dir string, size int) error {
	if !validRuntime(dir) {
		return fmt.Errorf("небезопасный путь RAM")
	}
	i, e := os.Lstat(dir)
	if e != nil {
		return e
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("RAM путь должен быть каталогом")
	}
	if e = syscall.Mount("alice-legacy-ram", dir, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "mode=0700,size="+strconv.Itoa(size)); e != nil {
		return e
	}
	return syscall.Mount("", dir, "", syscall.MS_REC|syscall.MS_PRIVATE, "")
}
func bindHost(root, source string, readonly bool) error {
	info, e := os.Stat(source)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	dest := filepath.Join(root, source)
	if info.IsDir() {
		e = os.MkdirAll(dest, 0700)
	} else {
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e == nil {
			var f *os.File
			f, e = os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e == nil {
				e = f.Close()
			}
		}
	}
	if e != nil {
		return e
	}
	if e = syscall.Mount(source, dest, "", syscall.MS_BIND, ""); e != nil {
		return e
	}
	if e = syscall.Mount("", dest, "", syscall.MS_PRIVATE, ""); e != nil {
		return e
	}
	if readonly {
		return syscall.Mount("", dest, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_NOSUID, "")
	}
	return nil
}
func mirrorSystem(root string) error {
	entries, e := os.ReadDir("/system")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Name() == "vendor" {
			continue
		}
		if e = bindHost(root, "/system/"+entry.Name(), true); e != nil {
			return e
		}
	}
	entries, e = os.ReadDir("/system/vendor")
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Name() == "quasar" {
			continue
		}
		if e = bindHost(root, "/system/vendor/"+entry.Name(), true); e != nil {
			return e
		}
	}
	return nil
}
func cleanupRuntime(root string) error {
	if root == "" {
		return nil
	}
	if !validRuntime(root) {
		return fmt.Errorf("unsafe cleanup path")
	}
	// Never recursively unlink a tree that may contain live /dev or /sys binds.
	e := syscall.Unmount(root, syscall.MNT_DETACH)
	if e != nil && e != syscall.EINVAL && e != syscall.ENOENT {
		return e
	}
	e = os.Remove(root)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func rootProcesses(root string) []procID {
	var result []procID
	for _, p := range processes() {
		r, e := os.Readlink(fmt.Sprintf("/proc/%d/root", p.PID))
		if e == nil && r == root {
			result = append(result, p)
		}
	}
	return result
}
func terminateRoot(root string) error {
	if root == "" {
		return nil
	}
	if !validRuntime(root) {
		return fmt.Errorf("invalid runtime root")
	}
	for attempt := 0; attempt < 10; attempt++ {
		ps := rootProcesses(root)
		if len(ps) == 0 {
			return nil
		}
		for _, p := range ps {
			signalProcess(p, syscall.SIGKILL)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("процессы нового стека не завершились")
}
func environmentMain(data, root string, probe bool) error {
	var out, control *os.File
	if !probe {
		out = os.NewFile(3, "status")
		control = os.NewFile(4, "control")
		defer out.Close()
		defer control.Close()
		// Arm recovery before creating any global mounts.
		if _, e := out.WriteString("SPAWNED\n"); e != nil {
			return e
		}
		var b [1]byte
		if _, e := control.Read(b[:]); e != nil || b[0] != 'P' {
			return fmt.Errorf("preparation cancelled")
		}
	}
	if e := ramMount(root, 80*1024*1024); e != nil {
		return e
	}
	defer cleanupRuntime(root)
	if probe {
		if e := os.WriteFile(filepath.Join(root, "probe-sentinel"), []byte("RAM only"), 0600); e != nil {
			return e
		}
		for _, source := range []string{"/bin", "/lib", "/usr"} {
			if e := bindHost(root, source, true); e != nil {
				return e
			}
		}
		cmd := exec.Command("/bin/busybox", "true")
		cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: root}
		cmd.Dir = "/"
		if e := cmd.Run(); e != nil {
			return e
		}
		fmt.Println("tmpfs, read-only bind mounts, ARM32 chroot execution and cleanup: OK")
		return nil
	}
	v := filepath.Join(root, vendor)
	if e := os.MkdirAll(v, 0700); e != nil {
		return e
	}
	if e := unpackPayload(filepath.Join(data, "framework.tar.gz"), v); e != nil {
		return e
	}
	for _, source := range []string{"/bin", "/sbin", "/lib", "/usr", "/etc"} {
		if e := bindHost(root, source, true); e != nil {
			return e
		}
	}
	if e := mirrorSystem(root); e != nil {
		return e
	}
	for _, source := range []string{"/dev", "/proc", "/sys"} {
		if e := bindHost(root, source, false); e != nil {
			return e
		}
	}
	for _, dir := range []string{"data", "bkupgrade", "tmp", "run", "root"} {
		if e := os.MkdirAll(filepath.Join(root, dir), 0700); e != nil {
			return e
		}
	}
	if e := os.Symlink("/data", filepath.Join(root, "bkupgrade/data")); e != nil {
		return e
	}
	if e := os.Chmod(filepath.Join(root, "tmp"), 01777); e != nil {
		return e
	}
	if e := syscall.Mkfifo(filepath.Join(root, "tmp/loop_capture_pcm"), 0600); e != nil {
		return e
	}
	if _, e := out.WriteString("PREPARED\n"); e != nil {
		return e
	}
	var goByte [1]byte
	if _, e := control.Read(goByte[:]); e != nil || goByte[0] != 'G' {
		return fmt.Errorf("activation cancelled")
	}
	if e := copyProfile("/data/quasar", filepath.Join(root, "data/quasar")); e != nil {
		return e
	}
	limits()
	cmd := exec.Command(vendor+"/quasar_launcher2", vendor+"/launch.json")
	cmd.Dir = "/data/quasar"
	cmd.Env = stockEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: root}
	null, e := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	defer null.Close()
	cmd.Stdin = null
	cmd.Stdout = null
	cmd.Stderr = null
	if e = cmd.Start(); e != nil {
		return e
	}
	defer terminateRoot(root)
	died := make(chan error, 1)
	go func() { died <- cmd.Wait() }()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil
	case <-died:
		return fmt.Errorf("новый launcher завершился")
	case <-timer.C:
	}
	if !hasRootRole(root, "--platformd") || !hasRootRole(root, "--mediad") {
		return fmt.Errorf("новые platformd/mediad не запустились")
	}
	if _, e = out.WriteString("RUNNING\n"); e != nil {
		return e
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	missing := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-died:
			return fmt.Errorf("новый launcher завершился")
		case <-ticker.C:
			if !hasRootRole(root, "--platformd") || !hasRootRole(root, "--mediad") {
				missing++
			} else {
				missing = 0
			}
			if missing >= 10 {
				return fmt.Errorf("новые голосовые процессы не восстановились")
			}
		}
	}
}
func hasRootRole(root, role string) bool {
	for _, p := range rootProcesses(root) {
		for _, arg := range p.Args {
			if arg == role && strings.HasSuffix(p.Exe, "/maind") {
				return true
			}
		}
	}
	return false
}
