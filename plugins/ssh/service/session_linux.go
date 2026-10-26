//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"syscall"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

var terminalName = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,64}$`)

type terminalRequest struct {
	Name                         string
	Columns, Rows, Width, Height uint32
	Modes                        string
}
type terminalSize struct{ Columns, Rows, Width, Height uint32 }
type sessionProcess struct {
	cmd       *exec.Cmd
	master    *os.File
	stdin     io.WriteCloser
	closeOnce sync.Once
}

func (p *sessionProcess) resize(size terminalSize) error {
	if p.master == nil {
		return errors.New("session has no terminal")
	}
	columns, rows := size.Columns, size.Rows
	if columns < 1 {
		columns = 80
	}
	if rows < 1 {
		rows = 24
	}
	if columns > 4096 || rows > 4096 {
		return errors.New("invalid terminal size")
	}
	return unix.IoctlSetWinsize(int(p.master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(columns), Row: uint16(rows)})
}

func (p *sessionProcess) close() {
	p.closeOnce.Do(func() {
		if p.cmd.Process != nil {
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
		}
		if p.master != nil {
			_ = p.master.Close()
		}
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		if p.cmd.Process != nil {
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		}
	})
}

func startProcess(ctx context.Context, ch ssh.Channel, terminal *terminalRequest, command *string, result chan<- uint32) (*sessionProcess, error) {
	args := []string{"-l"}
	if command != nil {
		args = []string{"-c", *command}
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", args...)
	cmd.Dir = "/data"
	term := "dumb"
	if terminal != nil {
		term = terminal.Name
	}
	cmd.Env = []string{"PATH=/system/workdir/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/data", "USER=root", "LOGNAME=root", "SHELL=/bin/sh", "LANG=C.UTF-8", "TERM=" + term}
	p := &sessionProcess{cmd: cmd}
	if terminal != nil {
		master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
		if err != nil {
			return nil, err
		}
		p.master = master
		if err = unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
			master.Close()
			return nil, err
		}
		number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
		if err != nil {
			master.Close()
			return nil, err
		}
		slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
		if err != nil {
			master.Close()
			return nil, err
		}
		defer slave.Close()
		if err = p.resize(terminalSize{terminal.Columns, terminal.Rows, terminal.Width, terminal.Height}); err != nil {
			master.Close()
			return nil, err
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0, Pdeathsig: syscall.SIGKILL}
	} else {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		p.stdin = stdin
		cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	}
	if err := cmd.Start(); err != nil {
		p.close()
		return nil, err
	}
	var copied chan struct{}
	if p.master != nil {
		copied = make(chan struct{})
		go func() { _, _ = io.Copy(ch, p.master); close(copied) }()
		go func() { _, _ = io.Copy(p.master, ch) }()
	} else {
		go func() { _, _ = io.Copy(p.stdin, ch); _ = p.stdin.Close() }()
	}
	go func() {
		err := cmd.Wait()
		status := uint32(0)
		if err != nil {
			status = 255
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				if code := exit.ExitCode(); code >= 0 {
					status = uint32(code)
				} else if state, ok := exit.Sys().(syscall.WaitStatus); ok && state.Signaled() {
					status = uint32(128 + state.Signal())
				}
			}
		}
		if copied != nil {
			<-copied
		}
		result <- status
	}()
	return p, nil
}

func serveSession(parent context.Context, ch ssh.Channel, requests <-chan *ssh.Request, allowSFTP bool) {
	ctx, cancel := context.WithCancel(parent)
	var process *sessionProcess
	defer func() {
		cancel()
		_ = ch.Close()
		if process != nil {
			process.close()
		}
	}()
	var terminal *terminalRequest
	started := false
	finished := make(chan uint32, 1)
	for {
		select {
		case <-ctx.Done():
			return
		case status := <-finished:
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
			return
		case request, ok := <-requests:
			if !ok {
				return
			}
			accepted := false
			switch request.Type {
			case "pty-req":
				var size terminalRequest
				if !started && terminal == nil && ssh.Unmarshal(request.Payload, &size) == nil && terminalName.MatchString(size.Name) && size.Columns <= 4096 && size.Rows <= 4096 {
					terminal = &size
					accepted = true
				}
			case "window-change":
				var size terminalSize
				if terminal != nil && ssh.Unmarshal(request.Payload, &size) == nil && size.Columns <= 4096 && size.Rows <= 4096 {
					if process == nil {
						terminal.Columns, terminal.Rows = size.Columns, size.Rows
						accepted = true
					} else {
						accepted = process.resize(size) == nil
					}
				}
			case "shell", "exec":
				if !started {
					var command *string
					valid := len(request.Payload) == 0
					if request.Type == "exec" {
						var arg struct{ Command string }
						valid = ssh.Unmarshal(request.Payload, &arg) == nil && len(arg.Command) <= 32768
						if valid {
							command = &arg.Command
						}
					}
					if valid {
						var err error
						process, err = startProcess(ctx, ch, terminal, command, finished)
						if err == nil {
							started = true
							accepted = true
						} else {
							_, _ = fmt.Fprintln(ch.Stderr(), "Could not start shell:", err)
						}
					}
				}
			case "subsystem":
				var arg struct{ Name string }
				if !started && terminal == nil && allowSFTP && ssh.Unmarshal(request.Payload, &arg) == nil && arg.Name == "sftp" {
					server, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory("/data"))
					if err == nil {
						started = true
						accepted = true
						go func() {
							err := server.Serve()
							_ = server.Close()
							status := uint32(0)
							if err != nil && err != io.EOF {
								status = 1
							}
							finished <- status
						}()
					}
				}
			case "signal":
				var arg struct{ Name string }
				if process != nil && process.cmd.Process != nil && ssh.Unmarshal(request.Payload, &arg) == nil {
					sig, ok := map[string]syscall.Signal{"INT": syscall.SIGINT, "TERM": syscall.SIGTERM, "HUP": syscall.SIGHUP, "QUIT": syscall.SIGQUIT, "KILL": syscall.SIGKILL}[arg.Name]
					if ok {
						accepted = syscall.Kill(-process.cmd.Process.Pid, sig) == nil
					}
				}
			}
			_ = request.Reply(accepted, nil)
		}
	}
}
