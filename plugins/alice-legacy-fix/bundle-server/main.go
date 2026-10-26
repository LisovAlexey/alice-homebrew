// MIT, Alice Homebrew contributors. Serves one verified file from this folder.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

//go:embed payload.json
var metadata []byte

func main() {
	if e := serve(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func serve() error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	name := filepath.Join(filepath.Dir(exe), "alice-framework.tar.gz")
	var spec struct {
		SHA  string `json:"payload_sha256"`
		Size int64  `json:"payload_bytes"`
	}
	if e = json.Unmarshal(metadata, &spec); e != nil {
		return e
	}
	f, e := os.Open(name)
	if e != nil {
		return fmt.Errorf("рядом с программой нужен alice-framework.tar.gz: %w", e)
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil {
		return e
	}
	if n != spec.Size || hex.EncodeToString(h.Sum(nil)) != spec.SHA {
		return fmt.Errorf("комплект повреждён, SHA-256 не совпал")
	}
	var token [16]byte
	if _, e = rand.Read(token[:]); e != nil {
		return e
	}
	path := "/" + hex.EncodeToString(token[:]) + "/alice-framework.tar.gz"
	listener, e := net.Listen("tcp4", ":18189")
	if e != nil {
		return e
	}
	defer listener.Close()
	fmt.Println("Alice Legacy Fix — локальная передача комплекта / Local bundle transfer")
	fmt.Println("Включите доступ в брандмауэре только для частной сети. Колонка и ПК должны быть в одной сети.")
	addresses, _ := net.InterfaceAddrs()
	for _, address := range addresses {
		ip, _, er := net.ParseCIDR(address.String())
		if er == nil && ip.To4() != nil && ip.IsPrivate() {
			fmt.Printf("Адрес для поля «Адрес комплекта»: http://%s:18189%s\n", ip, path)
		}
	}
	fmt.Println("Оставьте окно открытым до окончания загрузки. Ctrl+C — закрыть.")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) || r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// SectionReader supports concurrent ranges without sharing file offsets.
		http.ServeContent(w, r, "alice-framework.tar.gz", time.Time{}, io.NewSectionReader(f, 0, n))
	})
	server := http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second}
	return server.Serve(listener)
}
