package store

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const ext = ".alcf"

// Store persists module configurations as encrypted .alcf files.
type Store struct {
	dir string
	c   *Cipher
	mu  sync.Mutex
}

// Open prepares dir, loading (or creating) its master key.
func Open(dir, passphrase string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c, err := LoadOrCreateKey(dir, passphrase)
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, c: c}, nil
}

// Dir is the data directory.
func (s *Store) Dir() string { return s.dir }

// Path returns a path inside the data directory.
func (s *Store) Path(elem ...string) string {
	return filepath.Join(append([]string{s.dir}, elem...)...)
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// LoadRaw returns the decrypted JSON of a module, or nil if absent.
func (s *Store) LoadRaw(name string) ([]byte, error) {
	if !validName(name) {
		return nil, fmt.Errorf("invalid config name %q", name)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, name+ext))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.c.Open(name, data)
}

// Load decodes a module config into v. It reports whether a file existed.
func (s *Store) Load(name string, v any) (bool, error) {
	raw, err := s.LoadRaw(name)
	if err != nil || raw == nil {
		return false, err
	}
	return true, json.Unmarshal(raw, v)
}

// Save encrypts v as JSON into name.alcf.
func (s *Store) Save(name string, v any) error {
	if !validName(name) {
		return fmt.Errorf("invalid config name %q", name)
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	enc, err := s.c.Seal(name, raw)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeFileAtomic(filepath.Join(s.dir, name+ext), enc, 0o600)
}

// Names lists stored module names.
func (s *Store) Names() []string {
	entries, _ := os.ReadDir(s.dir)
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			out = append(out, strings.TrimSuffix(e.Name(), ext))
		}
	}
	sort.Strings(out)
	return out
}

// ExportZip writes every .alcf file plus master.key into a zip archive.
// When plain is true, decrypted JSON files are written instead (no key).
func (s *Store) ExportZip(w io.Writer, plain bool) error {
	zw := zip.NewWriter(w)
	now := time.Now()
	add := func(name string, data []byte) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	for _, n := range s.Names() {
		if plain {
			raw, err := s.LoadRaw(n)
			if err != nil {
				return err
			}
			if err := add(n+".json", raw); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, n+ext))
		if err != nil {
			return err
		}
		if err := add(n+ext, data); err != nil {
			return err
		}
	}
	if !plain {
		key, err := os.ReadFile(filepath.Join(s.dir, "master.key"))
		if err != nil {
			return err
		}
		if err := add("master.key", key); err != nil {
			return err
		}
	}
	return zw.Close()
}

// ImportZip restores a backup made by ExportZip. Encrypted backups must carry
// their master.key (and need the same passphrase); plain backups (.json) are
// re-encrypted with the current key. Every file is validated before anything
// is written.
func (s *Store) ImportZip(r io.ReaderAt, size int64, passphrase string) ([]string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.UncompressedSize64 > 64<<20 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		files[filepath.Base(f.Name)] = data
	}
	plainCfg := map[string][]byte{}
	if keyData, ok := files["master.key"]; ok {
		key, err := decodeMasterKey(keyData, passphrase)
		if err != nil {
			return nil, err
		}
		bc, err := newCipher(key)
		if err != nil {
			return nil, err
		}
		for name, data := range files {
			if !strings.HasSuffix(name, ext) {
				continue
			}
			mod := strings.TrimSuffix(name, ext)
			if !validName(mod) {
				continue
			}
			raw, err := bc.Open(mod, data)
			if err != nil {
				return nil, err
			}
			plainCfg[mod] = raw
		}
	}
	for name, data := range files {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		mod := strings.TrimSuffix(name, ".json")
		if !validName(mod) {
			continue
		}
		if !json.Valid(data) {
			return nil, fmt.Errorf("%s: invalid JSON", name)
		}
		plainCfg[mod] = data
	}
	if len(plainCfg) == 0 {
		return nil, errors.New("backup contains no configuration files")
	}
	var names []string
	for mod, raw := range plainCfg {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			return nil, fmt.Errorf("%s: %w", mod, err)
		}
		enc, err := s.c.Seal(mod, buf.Bytes())
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(filepath.Join(s.dir, mod+ext), enc, 0o600); err != nil {
			return nil, err
		}
		names = append(names, mod)
	}
	sort.Strings(names)
	return names, nil
}
