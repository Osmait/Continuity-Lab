package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesSPAAndAssets(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/", "/repos/acme/demo", "/admin", "/admin/wal"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("%s: status=%d body=%q", path, response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, firstAsset(t), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}

	request = httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") || !strings.Contains(response.Body.String(), "unregister") {
		t.Fatalf("cleanup worker: status=%d cache=%q body=%q", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
}

func firstAsset(t *testing.T) string {
	t.Helper()
	entries, err := assets.ReadDir("dist/assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("read embedded assets: %v", err)
	}
	return "/assets/" + entries[0].Name()
}
