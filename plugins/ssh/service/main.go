//go:build linux

// MIT, Alice Homebrew contributors.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

type configuration struct {
	Port      int    `json:"port"`
	Listen    string `json:"listen"`
	PublicKey string `json:"public-key"`
	Sessions  int    `json:"sessions"`
	SFTP      bool   `json:"sftp"`
}

func loadConfig() (configuration, ssh.PublicKey, error) {
	var c configuration
	raw, err := os.ReadFile(os.Getenv("ALICE_CONFIG_PATH"))
	if err != nil {
		return c, nil, err
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return c, nil, err
	}
	if c.Port < 1 || c.Port > 65535 || c.Sessions < 1 || c.Sessions > 4 || (c.Listen != "0.0.0.0" && c.Listen != "127.0.0.1") {
		return c, nil, errors.New("invalid SSH port, listen address or session limit")
	}
	if strings.TrimSpace(c.PublicKey) == "" {
		return c, nil, errors.New("Сначала вставьте публичный SSH-ключ в настройки / Set your SSH public key before enabling")
	}
	if len(c.PublicKey) > 1024 {
		return c, nil, errors.New("SSH public key is too long")
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(c.PublicKey))
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return c, nil, errors.New("paste one public SSH key without authorized_keys options")
	}
	if _, certificate := key.(*ssh.Certificate); certificate {
		return c, nil, errors.New("use a plain SSH public key, not a certificate")
	}
	return c, key, nil
}

func hostKey(data string) (ssh.Signer, error) {
	path := filepath.Join(data, "ssh_host_ed25519_key")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("SSH host key must be a regular file")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err = os.Chmod(path, 0600); err != nil {
			return nil, err
		}
		return ssh.ParsePrivateKey(raw)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = file.Write(encoded); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(key)
}

func run() error {
	if os.Geteuid() != 0 {
		return errors.New("SSH plugin requires the rooted speaker")
	}
	c, authorized, err := loadConfig()
	if err != nil {
		return err
	}
	data := os.Getenv("ALICE_PLUGIN_DATA")
	if data == "" {
		return errors.New("ALICE_PLUGIN_DATA is missing")
	}
	if err = os.MkdirAll(data, 0700); err != nil {
		return err
	}
	signer, err := hostKey(data)
	if err != nil {
		return err
	}
	config := &ssh.ServerConfig{MaxAuthTries: 3, ServerVersion: "SSH-2.0-AliceHomebrewSSH",
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if conn.User() != "root" || !bytes.Equal(key.Marshal(), authorized.Marshal()) {
				return nil, errors.New("public key denied")
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp4", net.JoinHostPort(c.Listen, fmt.Sprint(c.Port)))
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	var mu sync.Mutex
	connections := map[net.Conn]struct{}{}
	go func() {
		<-ctx.Done()
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for conn := range connections {
			conn.Close()
		}
	}()
	var workers sync.WaitGroup
	connectionSlots := make(chan struct{}, 4)
	sessionSlots := make(chan struct{}, c.Sessions)
	log.Printf("SSH ready: %s:%d, user root, public-key authentication, SFTP=%t", c.Listen, c.Port, c.SFTP)
	log.Printf("SSH host fingerprint: %s", ssh.FingerprintSHA256(signer.PublicKey()))
	for {
		conn, err := listener.Accept()
		if err != nil {
			stopping := ctx.Err() != nil
			cancel()
			workers.Wait()
			if stopping {
				return nil
			}
			return err
		}
		select {
		case connectionSlots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		mu.Lock()
		if ctx.Err() != nil {
			mu.Unlock()
			conn.Close()
			<-connectionSlots
			continue
		}
		connections[conn] = struct{}{}
		mu.Unlock()
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-connectionSlots }()
			defer conn.Close()
			defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
			_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
			server, channels, requests, err := ssh.NewServerConn(conn, config)
			if err != nil {
				return
			}
			defer server.Close()
			_ = conn.SetDeadline(time.Time{})
			go func() {
				for request := range requests {
					_ = request.Reply(request.Type == "keepalive@openssh.com", nil)
				}
			}()
			var sessions sync.WaitGroup
			for next := range channels {
				if next.ChannelType() != "session" {
					_ = next.Reject(ssh.Prohibited, "only shell, command and SFTP sessions are supported")
					continue
				}
				select {
				case sessionSlots <- struct{}{}:
				default:
					_ = next.Reject(ssh.ResourceShortage, "session limit reached")
					continue
				}
				channel, requests, err := next.Accept()
				if err != nil {
					<-sessionSlots
					continue
				}
				sessions.Add(1)
				go func() {
					defer sessions.Done()
					defer func() { <-sessionSlots }()
					serveSession(ctx, channel, requests, c.SFTP)
				}()
			}
			sessions.Wait()
		}()
	}
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
