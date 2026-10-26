package homebrew

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type failingLogRunner struct{ closed bool }

func (r *failingLogRunner) Tick(map[string]any, uint32) ([]byte, error) {
	return nil, errors.New("exit status 1")
}
func (r *failingLogRunner) Close() { r.closed = true }
func (r *failingLogRunner) LogLines() []string {
	return []string{"preparing", "download failed: test reason"}
}
func (r *failingLogRunner) MemoryBytes() uint32 { return 0 }

func TestPluginFailureRetainsLogsAfterRestart(t *testing.T) {
	m := manager(t)
	p := mustPackage(t, "1.0.0")
	staged, e := m.Stage(p.Raw, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Install(staged["ticket"].(string), nil, true); e != nil {
		t.Fatal(e)
	}
	runner := &failingLogRunner{}
	m.running[p.Manifest.ID] = runner
	m.state.Sources = nil
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot := m.Snapshot()
		plugin := snapshot["plugins"].([]*Installed)[0]
		if plugin.LastError != "" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("failure not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if !runner.closed {
		t.Fatal("runner not closed")
	}
	loaded, e := NewManager(m.dir, false, true)
	if e != nil {
		t.Fatal(e)
	}
	defer loaded.Ring.Close()
	for _, snapshot := range []map[string]any{m.Snapshot(), loaded.Snapshot()} {
		plugin := snapshot["plugins"].([]*Installed)[0]
		if len(plugin.Logs) != 2 || !strings.Contains(plugin.Logs[1], "test reason") || plugin.Enabled {
			t.Fatal("diagnostics lost", plugin)
		}
	}
	statuses := loaded.Status()["plugins"].([]map[string]any)
	if len(statuses) != 1 || len(statuses[0]["logs"].([]string)) != 2 {
		t.Fatal("poll response lost logs")
	}
}
func TestPluginLogBoundsOnLoad(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = strings.Repeat("x", 4096)
	}
	got := boundedLogLines(lines)
	if len(got) != 32 {
		t.Fatal(len(got))
	}
	for _, line := range got {
		if len(line) > 2048 {
			t.Fatal(len(line))
		}
	}
}
