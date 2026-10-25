package homebrew

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
)

const MaxPackage = 4 << 20
const MaxModule = 2 << 20
const APIVersion = 1

var idRE = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
var versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$`)
var colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type Text struct {
	RU string `json:"ru"`
	EN string `json:"en"`
}
type Option struct {
	Value string `json:"value"`
	Label Text   `json:"label"`
}
type Field struct {
	Key       string   `json:"key"`
	Label     Text     `json:"label"`
	Type      string   `json:"type"`
	Default   any      `json:"default"`
	Min       float64  `json:"min,omitempty"`
	Max       float64  `json:"max,omitempty"`
	Step      float64  `json:"step,omitempty"`
	Options   []Option `json:"options,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
}
type Manifest struct {
	Format       int      `json:"format"`
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Title        Text     `json:"title"`
	Description  Text     `json:"description"`
	Author       string   `json:"author"`
	API          int      `json:"api"`
	Permissions  []string `json:"permissions"`
	Config       []Field  `json:"config"`
	ModuleSHA256 string   `json:"module_sha256"`
}
type Package struct {
	Manifest Manifest
	Module   []byte
	Raw      []byte
	SHA256   string
}

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func decode(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("extra JSON data")
	}
	return nil
}
func ValidateManifest(m Manifest) error {
	if m.Format != 1 || m.API != APIVersion || len(m.ID) > 64 || len(m.ID) < 3 || !idRE.MatchString(m.ID) || !versionRE.MatchString(m.Version) {
		return errors.New("unsupported manifest format, ID, version or API")
	}
	if m.Title.RU == "" && m.Title.EN == "" {
		return errors.New("plugin title is missing")
	}
	if len(m.Title.RU)+len(m.Title.EN) > 400 || len(m.Description.RU)+len(m.Description.EN) > 4000 || len(m.Author) > 160 {
		return errors.New("manifest text is too long")
	}
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		if seen[p] || (p != "led.ring" && p != "log.write") {
			return fmt.Errorf("unsupported or duplicate permission: %s", p)
		}
		seen[p] = true
	}
	if len(m.Config) > 32 {
		return errors.New("too many configuration fields")
	}
	seen = map[string]bool{}
	for _, f := range m.Config {
		if !idRE.MatchString(f.Key) || len(f.Key) > 48 || seen[f.Key] {
			return errors.New("invalid or duplicate config key")
		}
		seen[f.Key] = true
		switch f.Type {
		case "range":
			if !finite(f.Min) || !finite(f.Max) || !finite(f.Step) || f.Max <= f.Min || f.Step <= 0 {
				return errors.New("invalid slider bounds")
			}
		case "select":
			if len(f.Options) < 1 || len(f.Options) > 32 {
				return errors.New("invalid select options")
			}
			values := map[string]bool{}
			for _, o := range f.Options {
				if o.Value == "" || len(o.Value) > 80 || values[o.Value] {
					return errors.New("invalid select option")
				}
				values[o.Value] = true
			}
		case "text":
			if f.MaxLength < 1 || f.MaxLength > 1024 {
				return errors.New("invalid text length")
			}
		case "color", "boolean":
		default:
			return fmt.Errorf("unsupported field type: %s", f.Type)
		}
		if err := validateValue(f, f.Default); err != nil {
			return fmt.Errorf("default %s: %w", f.Key, err)
		}
	}
	return nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validateValue(f Field, v any) error {
	switch f.Type {
	case "boolean":
		if _, ok := v.(bool); ok {
			return nil
		}
	case "color":
		if s, ok := v.(string); ok && colorRE.MatchString(s) {
			return nil
		}
	case "text":
		if s, ok := v.(string); ok && len([]rune(s)) <= f.MaxLength {
			return nil
		}
	case "select":
		if s, ok := v.(string); ok {
			for _, o := range f.Options {
				if s == o.Value {
					return nil
				}
			}
		}
	case "range":
		if n, ok := v.(float64); ok && finite(n) && n >= f.Min && n <= f.Max {
			steps := (n - f.Min) / f.Step
			if math.Abs(steps-math.Round(steps)) < 1e-6 {
				return nil
			}
		}
	}
	return errors.New("value does not match the configuration schema")
}
func Config(m Manifest, supplied map[string]any) (map[string]any, error) {
	out := map[string]any{}
	known := map[string]bool{}
	for _, f := range m.Config {
		known[f.Key] = true
		v, ok := supplied[f.Key]
		if !ok {
			v = f.Default
		}
		if err := validateValue(f, v); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Key, err)
		}
		out[f.Key] = v
	}
	for k := range supplied {
		if !known[k] {
			return nil, fmt.Errorf("unknown config key: %s", k)
		}
	}
	return out, nil
}
func Parse(raw []byte) (*Package, error) {
	if len(raw) > MaxPackage {
		return nil, errors.New("package exceeds 4 MiB")
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	var total uint64
	for _, f := range z.File {
		if f.Name != "manifest.json" && f.Name != "plugin.wasm" && f.Name != "README.md" && f.Name != "LICENSE" {
			return nil, fmt.Errorf("unexpected archive path: %s", f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 || f.FileInfo().IsDir() {
			return nil, errors.New("links and directories are not allowed")
		}
		if _, ok := files[f.Name]; ok {
			return nil, errors.New("duplicate archive entry")
		}
		total += f.UncompressedSize64
		if total > MaxPackage || f.UncompressedSize64 > MaxModule {
			return nil, errors.New("expanded package is too large")
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(r, MaxModule+1))
		r.Close()
		if e != nil {
			return nil, e
		}
		if len(b) > MaxModule {
			return nil, errors.New("archive entry is too large")
		}
		files[f.Name] = b
	}
	var m Manifest
	if err = decode(files["manifest.json"], &m); err != nil {
		return nil, err
	}
	if err = ValidateManifest(m); err != nil {
		return nil, err
	}
	wasm := files["plugin.wasm"]
	if len(wasm) < 8 || !bytes.Equal(wasm[:8], []byte{0, 97, 115, 109, 1, 0, 0, 0}) {
		return nil, errors.New("plugin.wasm is not WebAssembly version 1")
	}
	if m.ModuleSHA256 != Digest(wasm) {
		return nil, errors.New("module checksum mismatch")
	}
	return &Package{m, wasm, raw, Digest(raw)}, nil
}
func Pack(dir string) ([]byte, error) {
	raw, err := os.ReadFile(dir + "/manifest.json")
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err = decode(raw, &m); err != nil {
		return nil, err
	}
	wasm, err := os.ReadFile(dir + "/plugin.wasm")
	if err != nil {
		return nil, err
	}
	m.ModuleSHA256 = Digest(wasm)
	raw, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range []string{"manifest.json", "plugin.wasm", "README.md", "LICENSE"} {
		var data []byte
		if name == "manifest.json" {
			data = raw
		} else if name == "plugin.wasm" {
			data = wasm
		} else {
			data, err = os.ReadFile(dir + "/" + name)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
		}
		w, e := z.Create(name)
		if e != nil {
			return nil, e
		}
		if _, e = w.Write(data); e != nil {
			return nil, e
		}
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	if _, err = Parse(b.Bytes()); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func Has(m Manifest, permission string) bool {
	for _, p := range m.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}
func accepted(m Manifest, permissions []string) bool {
	a := append([]string{}, permissions...)
	if len(a) != len(m.Permissions) {
		return false
	}
	for _, p := range m.Permissions {
		found := false
		for i, v := range a {
			if v == p {
				a[i] = ""
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
