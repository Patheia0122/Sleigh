package http

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"sleigh-runtime/server/internal/config"
)

func TestHealthzReturnsOK(t *testing.T) {
	cfg := config.Config{
		HTTPAddr:        ":10122",
		ReadTimeout:     0,
		WriteTimeout:    0,
		ShutdownTimeout: 0,
		Version:         "test-version",
	}
	handler := NewHandler(cfg, nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content type application/json, got %q", got)
	}
}

func TestCodeWriteSingleFileRelativePath(t *testing.T) {
	dir := filepath.FromSlash("/workspace/pkg")
	file := filepath.FromSlash("/workspace/pkg/main.go")
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "main.go" {
		t.Fatalf("expected main.go, got %q", rel)
	}
}
