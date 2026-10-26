package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed payload.json
var manifestBytes []byte

type payloadSpec struct {
	PayloadSHA     string            `json:"payload_sha256"`
	PayloadBytes   int64             `json:"payload_bytes"`
	ExpandedBytes  int64             `json:"expanded_bytes"`
	Entries        int               `json:"entries"`
	DonorMain      string            `json:"donor_maind_sha256"`
	DonorLauncher  string            `json:"donor_launcher_sha256"`
	TargetMain     string            `json:"target_maind_sha256"`
	TargetLauncher string            `json:"target_launcher_sha256"`
	BaseFiles      map[string]string `json:"base_files"`
}

var spec = func() payloadSpec {
	var s payloadSpec
	if err := json.Unmarshal(manifestBytes, &s); err != nil {
		panic(err)
	}
	return s
}()

func digest(name string) (string, error) {
	f, e := os.Open(name)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func requireHash(name, want string) error {
	got, e := digest(name)
	if e != nil {
		return e
	}
	if got != want {
		return fmt.Errorf("неподдерживаемый файл %s (SHA-256 %s)", name, got)
	}
	return nil
}
func verifyPayload(name string) error {
	i, e := os.Lstat(name)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() || i.Size() != spec.PayloadBytes {
		return fmt.Errorf("неверный размер комплекта")
	}
	return requireHash(name, spec.PayloadSHA)
}
func cleanArchiveName(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("небезопасный путь архива")
	}
	for _, p := range strings.Split(name, "/") {
		if p == ".." {
			return "", fmt.Errorf("выход из архива")
		}
	}
	n := path.Clean(name)
	if n == "." {
		return "", fmt.Errorf("пустой путь архива")
	}
	return n, nil
}
func safeLink(name, target string) bool {
	if target == "" || path.IsAbs(target) || strings.Contains(target, "\\") {
		return false
	}
	p := path.Clean(path.Join(path.Dir(name), target))
	return p != ".." && !strings.HasPrefix(p, "../")
}

// Destination is a fresh private tmpfs. Links are created last; no archive entry
// may be the ancestor of another entry through a link or a regular file.
func unpackPayload(archive, dst string) error {
	if e := verifyPayload(archive); e != nil {
		return e
	}
	f, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, spec.ExpandedBytes+int64(spec.Entries)*2048+1048576))
	seen := map[string]byte{}
	links := map[string]string{}
	var total int64
	count := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		n, err := cleanArchiveName(h.Name)
		if err != nil {
			return err
		}
		if _, ok := seen[n]; ok {
			return fmt.Errorf("дублированный путь")
		}
		for p := path.Dir(n); p != "."; p = path.Dir(p) {
			if k, ok := seen[p]; ok && k != tar.TypeDir {
				return fmt.Errorf("небезопасный предок архива")
			}
		}
		if h.Typeflag != tar.TypeDir {
			for p := range seen {
				if strings.HasPrefix(p, n+"/") {
					return fmt.Errorf("небезопасный порядок архива")
				}
			}
		}
		count++
		if count > spec.Entries {
			return fmt.Errorf("слишком много записей")
		}
		seen[n] = h.Typeflag
		dest := filepath.Join(dst, filepath.FromSlash(n))
		mode := os.FileMode(h.Mode) & 0777
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(dest, mode)
		case tar.TypeReg, tar.TypeRegA:
			if h.Size < 0 || total+h.Size > spec.ExpandedBytes {
				return fmt.Errorf("слишком большой архив")
			}
			total += h.Size
			if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return err
			}
			var o *os.File
			o, err = os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err == nil {
				_, err = io.CopyN(o, tr, h.Size)
				ce := o.Close()
				if err == nil {
					err = ce
				}
			}
		case tar.TypeSymlink:
			if !safeLink(n, h.Linkname) {
				return fmt.Errorf("небезопасная ссылка")
			}
			links[n] = h.Linkname
		default:
			return fmt.Errorf("неподдерживаемая запись архива")
		}
		if err != nil {
			return err
		}
	}
	if total != spec.ExpandedBytes || count != spec.Entries {
		return fmt.Errorf("неполный архив")
	}
	for n, target := range links {
		if e = os.MkdirAll(filepath.Dir(filepath.Join(dst, n)), 0700); e != nil {
			return e
		}
		if e = os.Symlink(target, filepath.Join(dst, n)); e != nil {
			return e
		}
	}
	if e = requireHash(filepath.Join(dst, "maind"), spec.DonorMain); e != nil {
		return e
	}
	return requireHash(filepath.Join(dst, "quasar_launcher2"), spec.DonorLauncher)
}
