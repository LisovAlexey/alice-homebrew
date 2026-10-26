package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxProfileBytes int64 = 8 * 1024 * 1024

var skipProfile = map[string]bool{"logs": true, "daemons_logs": true, "fluent-bit": true, "cache": true, "ota": true, "tmp": true}

func profileLink(rel, target string) bool {
	if filepath.IsAbs(target) {
		target = filepath.Clean(target)
		return strings.HasPrefix(target, "/system/vendor/quasar/") || strings.HasPrefix(target, "/data/quasar/") || strings.HasPrefix(target, "/bkupgrade/data/quasar/")
	}
	p := filepath.Clean(filepath.Join(filepath.Dir(rel), target))
	return p != ".." && !strings.HasPrefix(p, "../")
}

// The recipient's identity stays local. Only a bounded RAM copy is mounted into
// the new framework; the old profile is never migrated or written by that stack.
func copyProfile(src, dst string) error {
	info, e := os.Lstat(src)
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return fmt.Errorf("профиль должен быть каталогом")
	}
	var total int64
	entries := 0
	e = filepath.WalkDir(src, func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		top := strings.Split(rel, string(filepath.Separator))[0]
		if skipProfile[top] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries++
		if entries > 4096 {
			return fmt.Errorf("слишком много файлов профиля")
		}
		i, err := d.Info()
		if err != nil {
			return err
		}
		dest := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, i.Mode().Perm())
		}
		if i.Mode()&os.ModeSymlink != 0 {
			target, er := os.Readlink(name)
			if er != nil {
				return er
			}
			if !profileLink(rel, target) {
				return fmt.Errorf("неподдерживаемая ссылка профиля: %s", rel)
			}
			return os.Symlink(target, dest)
		}
		// FIFOs and sockets are runtime objects, recreated inside the chroot.
		if !i.Mode().IsRegular() {
			return nil
		}
		total += i.Size()
		if total > maxProfileBytes {
			return fmt.Errorf("профиль больше 8 МиБ; исходник не изменён")
		}
		in, err := os.Open(name)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, i.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		n, err := io.Copy(out, io.LimitReader(in, i.Size()+1))
		in.Close()
		ce := out.Close()
		if err == nil {
			err = ce
		}
		if err != nil {
			return err
		}
		if n != i.Size() {
			return fmt.Errorf("профиль менялся при копировании")
		}
		return nil
	})
	if e != nil {
		return e
	}
	for _, dir := range []string{"logs", "daemons_logs", "fluent-bit", "cache", "tmp"} {
		if e = os.MkdirAll(filepath.Join(dst, dir), 0700); e != nil {
			return e
		}
	}
	return nil
}
