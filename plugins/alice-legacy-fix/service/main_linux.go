package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	Mode      string `json:"mode"`
	BundleURL string `json:"bundle-url"`
}

func main() {
	debug.SetMemoryLimit(24 * 1024 * 1024)
	log.SetFlags(0)
	var err error
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "child":
			if len(os.Args) != 4 {
				os.Exit(2)
			}
			err = environmentMain(os.Args[2], os.Args[3], false)
		case "watchdog":
			if len(os.Args) != 5 {
				os.Exit(2)
			}
			pid, e := strconv.Atoi(os.Args[2])
			if e != nil {
				os.Exit(2)
			}
			debug.SetMemoryLimit(8 * 1024 * 1024)
			err = watchdogMain(pid, os.Args[3], os.Args[4])
		case "probe":
			var root string
			root, err = os.MkdirTemp("/tmp", "alice-legacy-probe-")
			if err == nil {
				err = environmentMain("", root, true)
				if ce := cleanupRuntime(root); err == nil {
					err = ce
				}
			}
		default:
			err = fmt.Errorf("unknown command")
		}
	} else {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		err = run(ctx)
	}
	if err != nil {
		log.Printf("Остановлено / Stopped: %v", err)
		os.Exit(1)
	}
}
func environmentCommand(data, root string) *exec.Cmd {
	cmd := exec.Command(executable(), "child", data, root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd
}
func memoryAvailable() (int64, error) {
	raw, e := os.ReadFile("/proc/meminfo")
	if e != nil {
		return 0, e
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemAvailable:" {
			v, e := strconv.ParseInt(f[1], 10, 64)
			return v * 1024, e
		}
	}
	return 0, fmt.Errorf("MemAvailable недоступен")
}
func diskAvailable(dir string) (int64, error) {
	var st syscall.Statfs_t
	e := syscall.Statfs(dir, &st)
	return int64(st.Bavail) * st.Bsize, e
}
func basePreflight() error {
	if os.Geteuid() != 0 || runtime.GOARCH != "arm64" {
		return fmt.Errorf("нужен root на Mini 1, ARM64 Linux")
	}
	for name, want := range map[string]string{vendor + "/maind": spec.TargetMain, vendor + "/quasar_launcher2": spec.TargetLauncher} {
		if e := requireHash(name, want); e != nil {
			return e
		}
	}
	for name, want := range spec.BaseFiles {
		if e := requireHash(name, want); e != nil {
			return e
		}
	}
	board, e := os.ReadFile("/proc/device-tree/compatible")
	if e != nil || !strings.Contains(strings.ToLower(string(board)), "amlogic") {
		return fmt.Errorf("плата Amlogic не подтверждена")
	}
	raw, e := os.ReadFile(vendor + "/launch.json")
	if e != nil {
		return e
	}
	var launch any
	if e = json.Unmarshal(raw, &launch); e != nil {
		return e
	}
	encoded := string(raw)
	if !strings.Contains(encoded, `"--yiod"`) || !strings.Contains(encoded, `"--audiod"`) {
		return fmt.Errorf("ожидается старая схема yiod/audiod")
	}
	for _, name := range []string{"updatesd", "updater_switcher"} {
		if strings.Contains(encoded, name) {
			return fmt.Errorf("сначала отключите OTA штатным инструментом root; найден %s", name)
		}
	}
	hook, e := filepath.EvalSymlinks("/etc/init.d/S99onboot.sh")
	if e != nil || hook != vendor+"/onboot.sh" {
		return fmt.Errorf("штатный S99onboot.sh отключён или изменён")
	}
	if i, e := os.Stat("/data/quasar/initialized"); e != nil || !i.Mode().IsRegular() {
		return fmt.Errorf("Алиса ещё не настроена: initialized не найден")
	}
	return nil
}
func audioPreflight(ctx context.Context) error {
	req, e := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:8088/api/state", nil)
	if e != nil {
		return e
	}
	client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	r, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("не удалось проверить активные плагины Homebrew")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("Homebrew state: %d", r.StatusCode)
	}
	var state struct {
		Plugins []struct {
			Enabled  bool `json:"enabled"`
			Manifest struct {
				ID           string   `json:"id"`
				Capabilities []string `json:"capabilities"`
			} `json:"manifest"`
		} `json:"plugins"`
	}
	if e = json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&state); e != nil {
		return e
	}
	for _, p := range state.Plugins {
		if !p.Enabled || p.Manifest.ID == "alice-legacy-fix" || p.Manifest.ID == "ssh" {
			continue
		}
		for _, c := range p.Manifest.Capabilities {
			if strings.HasPrefix(c, "audio.") || strings.HasPrefix(c, "led.") {
				return fmt.Errorf("для первого запуска выключите плагин %s", p.Manifest.ID)
			}
		}
	}
	return nil
}
func localBundleURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, e
	}
	if u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("нужен HTTP-адрес локального сервера комплекта")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return nil, fmt.Errorf("адрес комплекта должен содержать локальный IP компьютера")
	}
	return u, nil
}
func obtainPayload(ctx context.Context, data, rawURL string) error {
	name := filepath.Join(data, "framework.tar.gz")
	if e := verifyPayload(name); e == nil {
		log.Print("Комплект уже сохранён; SHA-256 совпал")
		return nil
	} else if !os.IsNotExist(e) {
		return fmt.Errorf("сохранённый комплект повреждён; удалите framework.tar.gz по SFTP и повторите")
	}
	free, e := diskAvailable(data)
	if e != nil {
		return e
	}
	if free < spec.PayloadBytes+8*1024*1024 {
		return fmt.Errorf("нужно ещё %.1f МиБ на диске; удалите ненужные плагины", float64(spec.PayloadBytes+8*1024*1024-free)/1048576)
	}
	u, e := resolvePayloadURL(rawURL)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return e
	}
	if u.String() == officialBundleURL {
		log.Printf("Скачивание официального комплекта с GitHub: %.1f МиБ. Компьютер и bundle-server не нужны.", float64(spec.PayloadBytes)/1048576)
	} else {
		log.Print("Скачивание комплекта из локального источника")
	}
	r, e := payloadClient().Do(req)
	if e != nil {
		return payloadDownloadError(e)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("сервер комплекта: HTTP %d", r.StatusCode)
	}
	if r.ContentLength > spec.PayloadBytes {
		return fmt.Errorf("сервер вернул слишком большой комплект")
	}
	temp := name + ".partial"
	f, e := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(e) {
		if e = os.Remove(temp); e != nil {
			return e
		}
		f, e = os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	if e != nil {
		return e
	}
	defer os.Remove(temp)
	_, e = io.Copy(f, io.LimitReader(r.Body, spec.PayloadBytes+1))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = verifyPayload(temp); e != nil {
		return e
	}
	log.Print("Комплект скачан. SHA-256 совпал; сохраняю локальный кэш.")
	return os.Rename(temp, name)
}
func run(ctx context.Context) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	raw, e := os.ReadFile(os.Getenv("ALICE_CONFIG_PATH"))
	if e != nil {
		return e
	}
	var cfg config
	if e = json.Unmarshal(raw, &cfg); e != nil {
		return e
	}
	if cfg.Mode != "inspect" && cfg.Mode != "run-new" {
		return fmt.Errorf("неизвестный режим")
	}
	data := filepath.Clean(os.Getenv("ALICE_PLUGIN_DATA"))
	if !filepath.IsAbs(data) || filepath.Base(data) != "alice-legacy-fix" {
		return fmt.Errorf("неверный каталог данных Homebrew")
	}
	if e = os.MkdirAll(data, 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(data, "controller.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("плагин уже запущен")
	}
	log.Print("Alice Legacy Fix beta: проверка старой прошивки. Исправление диалога пока экспериментальное.")
	if e = basePreflight(); e != nil {
		return e
	}
	mem, e := memoryAvailable()
	if e != nil {
		return e
	}
	free, e := diskAvailable(data)
	if e != nil {
		return e
	}
	log.Printf("Совместимый исходный комплект. Свободно: RAM %.1f МиБ; диск %.1f МиБ", float64(mem)/1048576, float64(free)/1048576)
	if cfg.Mode == "inspect" {
		log.Print("Режим проверки: Алиса не изменена. Для активации нужен локальный комплект и режим «Запустить новый стек».")
		<-ctx.Done()
		return nil
	}
	if mem < 96*1024*1024 {
		return fmt.Errorf("нужно минимум 96 МиБ доступной RAM; выключите лишние плагины")
	}
	if e = audioPreflight(ctx); e != nil {
		return e
	}
	log.Print("Подготовка локального комплекта; штатная Алиса пока работает")
	if e = obtainPayload(ctx, data, cfg.BundleURL); e != nil {
		return e
	}
	// Wait until boot scripts have finished before taking over from S99.
	for {
		raw, e = os.ReadFile("/proc/uptime")
		if e != nil {
			return e
		}
		v, _ := strconv.ParseFloat(strings.Fields(string(raw))[0], 64)
		if v >= 60 {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	if e = basePreflight(); e != nil {
		return e
	}
	if e = audioPreflight(ctx); e != nil {
		return e
	}
	self, e := readProcess(os.Getpid())
	if e != nil {
		return e
	}
	root, e := os.MkdirTemp("/tmp", "alice-legacy-")
	if e != nil {
		return e
	}
	defer os.Remove(root)
	statusR, statusW, e := os.Pipe()
	if e != nil {
		return e
	}
	defer statusR.Close()
	controlR, controlW, e := os.Pipe()
	if e != nil {
		statusW.Close()
		return e
	}
	defer controlW.Close()
	child := environmentCommand(data, root)
	child.ExtraFiles = []*os.File{statusW, controlR}
	child.Stderr = os.Stderr
	e = child.Start()
	statusW.Close()
	controlR.Close()
	if e != nil {
		return fmt.Errorf("ядро не разрешило отдельное окружение: %w", e)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	defer func() {
		_ = child.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()
	messages := make(chan string, 2)
	go func() {
		scanner := bufio.NewScanner(statusR)
		for scanner.Scan() {
			messages <- scanner.Text()
		}
	}()
	if e = waitStatus(ctx, messages, done, "SPAWNED", 10*time.Second); e != nil {
		return e
	}
	childID, e := readProcess(child.Process.Pid)
	if e != nil {
		return e
	}
	life, watch, e := armWatchdog(childID, root)
	if e != nil {
		return e
	}
	// Always restore the original stack when this controller exits, even if the
	// manager kills it before deferred cleanup finishes.
	defer func() {
		_ = child.Process.Kill()
		_ = life.Close()
		select {
		case <-watch:
		case <-time.After(time.Second):
		}
	}()
	if _, e = controlW.Write([]byte("P")); e != nil {
		return e
	}
	if e = waitStatus(ctx, messages, done, "PREPARED", 2*time.Minute); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return nil
	}
	log.Print("Комплект проверен. Переключение на RAM-копию; исходные /system и профиль сохранены.")
	if e = stopStock(self.NS); e != nil {
		return e
	}
	if _, e = controlW.Write([]byte("G")); e != nil {
		return e
	}
	controlW.Close()
	if e = waitStatus(ctx, messages, done, "RUNNING", 60*time.Second); e != nil {
		return e
	}
	log.Print("Новые platformd и mediad запущены. Проверьте голосовой ответ и музыку. Выключение плагина вернёт исходную Алису.")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-done:
			return fmt.Errorf("новый стек завершился; возврат к исходному")
		case <-watch:
			return fmt.Errorf("сторож восстановления завершился")
		case <-ticker.C:
			if len(voiceProcesses(self.NS)) > 0 {
				return fmt.Errorf("штатный стек сам перезапустился; возврат к исходному")
			}
		}
	}
}
func waitStatus(ctx context.Context, messages <-chan string, done <-chan error, want string, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return fmt.Errorf("отдельное окружение завершилось до %s", want)
	case <-timer.C:
		return fmt.Errorf("таймаут %s", want)
	case got := <-messages:
		if got != want {
			return fmt.Errorf("неожиданный статус окружения")
		}
		return nil
	}
}
func armWatchdog(child procID, root string) (*os.File, <-chan error, error) {
	r, w, e := os.Pipe()
	if e != nil {
		return nil, nil, e
	}
	ackR, ackW, e := os.Pipe()
	if e != nil {
		r.Close()
		w.Close()
		return nil, nil, e
	}
	cmd := exec.Command(executable(), "watchdog", strconv.Itoa(child.PID), child.Start, root)
	cmd.Dir = "/"
	cmd.ExtraFiles = []*os.File{r, ackW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	e = cmd.Start()
	r.Close()
	ackW.Close()
	if e != nil {
		w.Close()
		ackR.Close()
		return nil, nil, e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if _, e = w.Write([]byte("A")); e == nil {
		_ = ackR.SetReadDeadline(time.Now().Add(5 * time.Second))
		var b [1]byte
		_, e = ackR.Read(b[:])
		if e == nil && b[0] != 'A' {
			e = fmt.Errorf("сторож не подтвердил готовность")
		}
	}
	ackR.Close()
	if e != nil {
		w.Close()
		return nil, nil, e
	}
	return w, done, nil
}
