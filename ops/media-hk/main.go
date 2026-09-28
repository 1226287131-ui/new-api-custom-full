package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultRoot          = "/var/lib/newapi-media"
	defaultAddr          = "127.0.0.1:8091"
	defaultImageLimit    = int64(50 << 20)
	defaultVideoLimit    = int64(1024 << 20)
	defaultImageLifetime = 2 * time.Hour
	defaultVideoLifetime = 48 * time.Hour
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,250}$`)

type mediaKind struct {
	name      string
	maxBytes  int64
	lifetime  time.Duration
	fileTypes map[string]bool
}

var kinds = map[string]mediaKind{
	"image-cache": {"image-cache", defaultImageLimit, defaultImageLifetime, map[string]bool{".avif": true, ".gif": true, ".jpg": true, ".jpeg": true, ".png": true, ".webp": true}},
	"video-cache": {"video-cache", defaultVideoLimit, defaultVideoLifetime, map[string]bool{".mp4": true}},
}

type server struct {
	root  string
	token []byte
	addr  string
}

func main() {
	s, err := newServerFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	if err := s.prepare(); err != nil {
		log.Fatal(err)
	}
	go s.cleanupLoop()
	log.Printf("media ingest listening on %s, root %s", s.addr, s.root)
	httpServer := &http.Server{
		Addr:              s.addr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func newServerFromEnv() (*server, error) {
	token := strings.TrimSpace(os.Getenv("MEDIA_HK_INGEST_TOKEN"))
	if token == "" {
		return nil, errors.New("MEDIA_HK_INGEST_TOKEN must be set")
	}
	root := strings.TrimSpace(os.Getenv("MEDIA_INGEST_ROOT"))
	if root == "" {
		root = defaultRoot
	}
	addr := strings.TrimSpace(os.Getenv("MEDIA_INGEST_ADDR"))
	if addr == "" {
		addr = defaultAddr
	}
	return &server{root: filepath.Clean(root), token: []byte(token), addr: addr}, nil
}

func (s *server) prepare() error {
	for name := range kinds {
		if err := os.MkdirAll(filepath.Join(s.root, name), 0750); err != nil {
			return fmt.Errorf("create %s directory: %w", name, err)
		}
	}
	s.cleanup()
	return nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[0] == "__media_ingest" && r.Method == http.MethodPut {
		s.handleUpload(w, r, parts[1], parts[2])
		return
	}
	if len(parts) == 2 && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		s.handleMedia(w, r, parts[0], parts[1])
		return
	}
	http.NotFound(w, r)
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request, kindName, filename string) {
	kind, ok := kinds[kindName]
	if !ok || !validFilename(filename, kind) {
		http.Error(w, "invalid media path", http.StatusBadRequest)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	expected := strings.TrimSpace(r.Header.Get("X-Content-SHA256"))
	if len(expected) != sha256.Size*2 {
		http.Error(w, "missing or invalid X-Content-SHA256", http.StatusBadRequest)
		return
	}
	if _, err := hex.DecodeString(expected); err != nil {
		http.Error(w, "invalid X-Content-SHA256", http.StatusBadRequest)
		return
	}
	if r.ContentLength > kind.maxBytes {
		http.Error(w, "media is too large", http.StatusRequestEntityTooLarge)
		return
	}
	// A missing Content-Length is valid for streamed uploads; LimitReader below
	// still enforces the per-kind cap without buffering the request in memory.
	dir := filepath.Join(s.root, kind.name)
	if err := os.MkdirAll(dir, 0750); err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer tmp.Close()
	hash := sha256.New()
	reader := io.LimitReader(io.TeeReader(r.Body, hash), kind.maxBytes+1)
	written, copyErr := io.Copy(tmp, reader)
	if copyErr != nil {
		http.Error(w, "upload failed", http.StatusBadRequest)
		return
	}
	if written <= 0 || written > kind.maxBytes {
		http.Error(w, "media is too large or empty", http.StatusRequestEntityTooLarge)
		return
	}
	if !constantTimeHexEqual(expected, hash.Sum(nil)) {
		http.Error(w, "content hash mismatch", http.StatusBadRequest)
		return
	}
	if err := tmp.Sync(); err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	if err := tmp.Close(); err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	path := filepath.Join(dir, filename)
	if err := os.Rename(tmpPath, path); err != nil {
		http.Error(w, "storage unavailable", http.StatusInternalServerError)
		return
	}
	_ = os.Chmod(path, 0640)
	w.WriteHeader(http.StatusCreated)
}

func (s *server) handleMedia(w http.ResponseWriter, r *http.Request, kindName, filename string) {
	kind, ok := kinds[kindName]
	if !ok || !validFilename(filename, kind) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.root, kind.name, filename)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, filename, info.ModTime(), f)
}

func (s *server) authorized(r *http.Request) bool {
	value := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	provided := []byte(strings.TrimSpace(strings.TrimPrefix(value, prefix)))
	providedHash := sha256.Sum256(provided)
	tokenHash := sha256.Sum256(s.token)
	equalLength := subtle.ConstantTimeEq(int32(len(provided)), int32(len(s.token))) == 1
	equalValue := subtle.ConstantTimeCompare(providedHash[:], tokenHash[:])
	return equalLength && equalValue == 1
}

func validFilename(filename string, kind mediaKind) bool {
	if !safeName.MatchString(filename) || filename == "." || filename == ".." {
		return false
	}
	ext := strings.ToLower(filepath.Ext(filename))
	return kind.fileTypes[ext]
}

func constantTimeHexEqual(expected string, actual []byte) bool {
	decoded, err := hex.DecodeString(expected)
	return err == nil && len(decoded) == len(actual) && subtle.ConstantTimeCompare(decoded, actual) == 1
}

func (s *server) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.cleanup()
	}
}

func (s *server) cleanup() {
	now := time.Now()
	for name, kind := range kinds {
		dir := filepath.Join(s.root, name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			lifetime := kind.lifetime
			if strings.HasPrefix(entry.Name(), ".upload-") {
				lifetime = time.Hour
			}
			if now.Sub(info.ModTime()) <= lifetime {
				continue
			}
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
