package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const vendor = "/system/vendor/quasar"

type procID struct {
	PID            int
	Start, NS, Exe string
	Args           []string
}

func readProcess(pid int) (procID, error) {
	p := procID{PID: pid}
	base := fmt.Sprintf("/proc/%d", pid)
	raw, e := os.ReadFile(base + "/stat")
	if e != nil {
		return p, e
	}
	s := string(raw)
	i := strings.LastIndex(s, ") ")
	if i < 0 {
		return p, fmt.Errorf("invalid proc stat")
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) < 20 || fields[0] == "Z" {
		return p, fmt.Errorf("process exited")
	}
	p.Start = fields[19]
	if p.NS, e = os.Readlink(base + "/ns/mnt"); e != nil {
		return p, e
	}
	p.Exe, _ = os.Readlink(base + "/exe")
	cmd, _ := os.ReadFile(base + "/cmdline")
	for _, a := range bytes.Split(bytes.TrimRight(cmd, "\x00"), []byte{0}) {
		p.Args = append(p.Args, string(a))
	}
	return p, nil
}
func processes() []procID {
	dirs, _ := os.ReadDir("/proc")
	var r []procID
	for _, d := range dirs {
		pid, e := strconv.Atoi(d.Name())
		if e != nil {
			continue
		}
		p, e := readProcess(pid)
		if e == nil {
			r = append(r, p)
		}
	}
	return r
}
func sameProcess(p procID) bool {
	now, e := readProcess(p.PID)
	return e == nil && now.Start == p.Start && now.NS == p.NS && now.Exe == p.Exe
}
func signalProcess(p procID, s syscall.Signal) {
	if sameProcess(p) {
		_ = syscall.Kill(p.PID, s)
	}
}
func voiceProcesses(ns string) []procID {
	var r []procID
	for _, p := range processes() {
		if p.NS == ns && strings.HasPrefix(p.Exe, vendor+"/") {
			r = append(r, p)
		}
	}
	return r
}
func stopStock(ns string) error {
	// Stop supervisors before their workers, then rescan. Never match by a short
	// process name: SSH, Homebrew and host Wi-Fi daemons are outside this path.
	for _, p := range voiceProcesses(ns) {
		if p.Exe == vendor+"/quasar_launcher2" {
			signalProcess(p, syscall.SIGSTOP)
		}
	}
	for _, p := range voiceProcesses(ns) {
		signalProcess(p, syscall.SIGTERM)
		signalProcess(p, syscall.SIGCONT)
	}
	time.Sleep(300 * time.Millisecond)
	for _, p := range voiceProcesses(ns) {
		signalProcess(p, syscall.SIGKILL)
	}
	time.Sleep(200 * time.Millisecond)
	if len(voiceProcesses(ns)) != 0 {
		return fmt.Errorf("штатные процессы не остановлены")
	}
	return nil
}
func hasRole(role string) bool {
	for _, p := range processes() {
		if p.Exe == vendor+"/maind" {
			for _, arg := range p.Args {
				if arg == role {
					return true
				}
			}
		}
	}
	return false
}
func stockEnv() []string {
	return []string{"HOME=/root", "USER=root", "LANG=C", "PATH=/system/vendor/quasar:/system/workdir/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LD_LIBRARY_PATH=/system/vendor/quasar/libs", "GST_PLUGIN_PATH=/system/vendor/quasar/libs", "CA_CERTIFICATES=/system/vendor/quasar/ca-certificates.crt", "SSL_CERT_FILE=/system/vendor/quasar/ca-certificates.crt", "MALLOC_ARENA_MAX=2"}
}
func limits() {
	_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: 10240, Max: 10240})
	_ = syscall.Setrlimit(syscall.RLIMIT_STACK, &syscall.Rlimit{Cur: 1024 * 1024, Max: 1024 * 1024})
}
func restartStock() error {
	if e := requireHash(vendor+"/maind", spec.TargetMain); e != nil {
		return e
	}
	if e := requireHash(vendor+"/quasar_launcher2", spec.TargetLauncher); e != nil {
		return e
	}
	ns, e := os.Readlink("/proc/self/ns/mnt")
	if e != nil {
		return e
	}
	for _, p := range voiceProcesses(ns) {
		if p.Exe == vendor+"/quasar_launcher2" {
			return nil
		}
	}
	limits()
	cmd := exec.Command(vendor+"/quasar_launcher2", vendor+"/launch.json")
	cmd.Dir = "/data/quasar"
	cmd.Env = stockEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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
	return cmd.Process.Release()
}

// A separate session and EOF pipe make rollback survive the manager's 1.5s
// process-group termination. PID/starttime verification prevents PID reuse.
func watchdogMain(pid int, start, root string) error {
	life := os.NewFile(3, "lifeline")
	ready := os.NewFile(4, "ready")
	defer life.Close()
	defer ready.Close()
	e := watchdogProtocol(life, ready, pid, start, func() error {
		if er := terminateRoot(root); er != nil {
			return er
		}
		cleanupErr := cleanupRuntime(root)
		return errors.Join(cleanupErr, restartStock())
	})
	status := "original launcher restored\n"
	if e != nil {
		status = "restore failed: " + e.Error() + "\n"
	}
	f, er := os.OpenFile("/data/alice-homebrew/alice-legacy-recovery.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if er == nil {
		_, _ = f.WriteString(time.Now().UTC().Format(time.RFC3339) + " " + status)
		_ = f.Close()
	}
	return e
}
func watchdogProtocol(life io.Reader, ready io.Writer, pid int, start string, restore func() error) error {
	var b [1]byte
	if _, e := life.Read(b[:]); e != nil || b[0] != 'A' {
		return fmt.Errorf("watchdog not armed")
	}
	if _, e := ready.Write([]byte("A")); e != nil {
		return e
	}
	for {
		if _, e := life.Read(b[:]); e != nil {
			break
		}
	}
	p, e := readProcess(pid)
	if e == nil && p.Start == start {
		signalProcess(p, syscall.SIGKILL)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p, e = readProcess(pid)
		if e != nil || p.Start != start {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if p, e = readProcess(pid); e == nil && p.Start == start {
		return fmt.Errorf("helper did not exit; refusing concurrent stock audio")
	}
	time.Sleep(500 * time.Millisecond)
	return restore()
}
func executable() string {
	p, e := os.Executable()
	if e != nil {
		panic(e)
	}
	return filepath.Clean(p)
}
