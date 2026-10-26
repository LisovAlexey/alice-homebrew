package main

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Pinned firmware content, independent of the bootstrap plugin version.
const officialBundleURL = "https://github.com/hertebeznat-cell/alice-homebrew/releases/download/alice-legacy-fix-v0.1.0-beta.1/alice-framework.tar.gz"

// Mozilla CA roots; MPL-2.0. Source and license accompany this plugin.
//
//go:embed certs/cacert.pem
var downloadCAPEM []byte

func resolvePayloadURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == officialBundleURL {
		return url.Parse(officialBundleURL)
	}
	return localBundleURL(raw)
}
func allowedPayloadRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("слишком много перенаправлений")
	}
	if len(via) == 0 || via[0].URL.String() != officialBundleURL {
		return errors.New("перенаправление локального источника запрещено")
	}
	if req.URL.Scheme != "https" || req.URL.User != nil {
		return errors.New("небезопасное перенаправление")
	}
	switch req.URL.Host {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	}
	return errors.New("перенаправление за пределы GitHub запрещено")
}
func payloadClient() *http.Client {
	roots, _ := x509.SystemCertPool()
	if roots == nil {
		roots = x509.NewCertPool()
	} else {
		roots = roots.Clone()
	}
	if !roots.AppendCertsFromPEM(downloadCAPEM) {
		panic("invalid bundled download CA")
	}
	return &http.Client{Timeout: 10 * time.Minute, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, CheckRedirect: allowedPayloadRedirect}
}
func payloadDownloadError(err error) error {
	var address *url.Error
	if errors.As(err, &address) {
		err = address.Err
	}
	return fmt.Errorf("не удалось скачать комплект; проверьте интернет колонки: %v", err)
}
