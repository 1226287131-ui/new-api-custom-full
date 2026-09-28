package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	return &server{root: t.TempDir(), token: []byte("test-token")}
}

func uploadRequest(t *testing.T, s *server, kind, name, body string, token string) *httptest.ResponseRecorder {
	t.Helper()
	hash := sha256.Sum256([]byte(body))
	r := httptest.NewRequest(http.MethodPut, "/__media_ingest/"+kind+"/"+name, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Content-SHA256", hex.EncodeToString(hash[:]))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestUploadRequiresAuthAndHash(t *testing.T) {
	s := newTestServer(t)
	w := uploadRequest(t, s, "image-cache", "asset.png", "abc", "wrong")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	r := httptest.NewRequest(http.MethodPut, "/__media_ingest/image-cache/asset.png", strings.NewReader("abc"))
	r.Header.Set("Authorization", "Bearer test-token")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUploadRejectsBodyHashMismatch(t *testing.T) {
	s := newTestServer(t)
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, "/__media_ingest/image-cache/asset.png", strings.NewReader("abc"))
	r.Header.Set("Authorization", "Bearer test-token")
	r.Header.Set("X-Content-SHA256", strings.Repeat("0", sha256.Size*2))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if _, err := os.Stat(filepath.Join(s.root, "image-cache", "asset.png")); !os.IsNotExist(err) {
		t.Fatalf("invalid payload was committed, stat error = %v", err)
	}
}

func TestUploadAtomicAndRangeRead(t *testing.T) {
	s := newTestServer(t)
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	w := uploadRequest(t, s, "video-cache", "task_123.mp4", "0123456789", "test-token")
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.root, "video-cache", "task_123.mp4")); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/video-cache/task_123.mp4", nil)
	r.Header.Set("Range", "bytes=2-5")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "2345" {
		t.Fatalf("range response = %d %q", w.Code, w.Body.String())
	}
}

func TestHeadReadAndMissingMedia(t *testing.T) {
	s := newTestServer(t)
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	if w := uploadRequest(t, s, "image-cache", "asset.png", "abc", "test-token"); w.Code != http.StatusCreated {
		t.Fatalf("upload status = %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodHead, "/image-cache/asset.png", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "3" {
		t.Fatalf("head response = %d body=%d content-length=%q", w.Code, w.Body.Len(), w.Header().Get("Content-Length"))
	}
	r = httptest.NewRequest(http.MethodGet, "/image-cache/missing.png", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing response = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestRejectsTraversalAndWrongExtension(t *testing.T) {
	s := newTestServer(t)
	if w := uploadRequest(t, s, "image-cache", "../asset.png", "abc", "test-token"); w.Code != http.StatusBadRequest {
		t.Fatalf("traversal status = %d", w.Code)
	}
	if w := uploadRequest(t, s, "video-cache", "asset.png", "abc", "test-token"); w.Code != http.StatusBadRequest {
		t.Fatalf("extension status = %d", w.Code)
	}
}

func TestCleanupRemovesExpiredMedia(t *testing.T) {
	s := newTestServer(t)
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.root, "image-cache", "old.png")
	if err := os.WriteFile(path, []byte("old"), 0640); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-defaultImageLifetime - time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	s.cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired file still exists, err=%v", err)
	}
}
