package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestEmptyURLUsesPinnedOfficialBundle(t *testing.T) {
	u, e := resolvePayloadURL("  ")
	if e != nil || u.String() != officialBundleURL {
		t.Fatal(u, e)
	}
	for _, raw := range []string{"https://evil.example/file", "http://8.8.8.8/file", "https://github.com/unrelated/file"} {
		if _, e = resolvePayloadURL(raw); e == nil {
			t.Fatal("untrusted source accepted", raw)
		}
	}
}
func TestPayloadRedirectPolicy(t *testing.T) {
	original, _ := http.NewRequest("GET", officialBundleURL, nil)
	for _, raw := range []string{"http://release-assets.githubusercontent.com/file", "https://evil.example/file", "https://github.com.evil.example/file", "https://github.com:444/file"} {
		req, _ := http.NewRequest("GET", raw, nil)
		if allowedPayloadRedirect(req, []*http.Request{original}) == nil {
			t.Fatal("unsafe redirect", raw)
		}
	}
	req, _ := http.NewRequest("GET", "https://release-assets.githubusercontent.com/file?temporary=redacted", nil)
	if e := allowedPayloadRedirect(req, []*http.Request{original}); e != nil {
		t.Fatal(e)
	}
	local, _ := http.NewRequest("GET", "http://192.168.1.2/file", nil)
	if allowedPayloadRedirect(req, []*http.Request{local}) == nil {
		t.Fatal("LAN redirect accepted")
	}
}
func TestPayloadTLSRejectsUnknownCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) }))
	defer server.Close()
	if response, e := payloadClient().Get(server.URL); e == nil {
		response.Body.Close()
		t.Fatal("untrusted TLS certificate accepted")
	}
	e := payloadDownloadError(&url.Error{Op: "Get", URL: "https://example.test/?private-token=secret", Err: context.DeadlineExceeded})
	if e.Error() != "не удалось скачать комплект; проверьте интернет колонки: context deadline exceeded" {
		t.Fatal(e)
	}
}
func TestDownloadHashBeforeCacheAndReuse(t *testing.T) {
	old := spec
	defer func() { spec = old }()
	raw := []byte("test payload")
	hash := sha256.Sum256(raw)
	spec.PayloadSHA = hex.EncodeToString(hash[:])
	spec.PayloadBytes = int64(len(raw))
	var bad atomic.Bool
	bad.Store(true)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if bad.Load() {
			w.Write([]byte("bad contents"))
		} else {
			w.Write(raw)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	if e := obtainPayload(context.Background(), dir, server.URL); e == nil {
		t.Fatal("bad hash accepted")
	}
	if _, e := os.Stat(filepath.Join(dir, "framework.tar.gz")); !os.IsNotExist(e) {
		t.Fatal("bad cache committed")
	}
	bad.Store(false)
	if e := obtainPayload(context.Background(), dir, server.URL); e != nil {
		t.Fatal(e)
	}
	if e := obtainPayload(context.Background(), dir, ""); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 2 {
		t.Fatal("cache not reused", requests.Load())
	}
}

func TestOfficialBundleDownload(t *testing.T) {
	if os.Getenv("ALICE_TEST_OFFICIAL_DOWNLOAD") == "" {
		t.Skip("set ALICE_TEST_OFFICIAL_DOWNLOAD for real public asset download")
	}
	if e := obtainPayload(context.Background(), t.TempDir(), ""); e != nil {
		t.Fatal(e)
	}
}
