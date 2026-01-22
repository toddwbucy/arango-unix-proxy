package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestBuildUpstreamURL(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		query    string
		expected string
	}{
		{
			name:     "simple path",
			path:     "/_api/version",
			query:    "",
			expected: "http://arangodb/_api/version",
		},
		{
			name:     "path with query",
			path:     "/_api/cursor",
			query:    "batchSize=100",
			expected: "http://arangodb/_api/cursor?batchSize=100",
		},
		{
			name:     "database-prefixed path",
			path:     "/_db/mydb/_api/document/collection",
			query:    "",
			expected: "http://arangodb/_db/mydb/_api/document/collection",
		},
		{
			name:     "path with complex query",
			path:     "/_api/cursor/12345",
			query:    "waitForSync=true&returnNew=true",
			expected: "http://arangodb/_api/cursor/12345?waitForSync=true&returnNew=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://localhost"+tt.path, nil)
			req.URL.RawQuery = tt.query
			result := buildUpstreamURL(req)
			if result != tt.expected {
				t.Errorf("buildUpstreamURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestStripHopHeaders(t *testing.T) {
	tests := []struct {
		name            string
		headers         http.Header
		expectRemoved   []string
		expectPreserved []string
	}{
		{
			name: "removes standard hop-by-hop headers",
			headers: http.Header{
				"Connection":        []string{"keep-alive"},
				"Keep-Alive":        []string{"timeout=5"},
				"Transfer-Encoding": []string{"chunked"},
				"Content-Type":      []string{"application/json"},
			},
			expectRemoved:   []string{"Connection", "Keep-Alive", "Transfer-Encoding"},
			expectPreserved: []string{"Content-Type"},
		},
		{
			name: "removes headers listed in Connection",
			headers: http.Header{
				"Connection":      []string{"X-Custom-Header, X-Another"},
				"X-Custom-Header": []string{"value1"},
				"X-Another":       []string{"value2"},
				"X-Preserved":     []string{"value3"},
			},
			expectRemoved:   []string{"Connection", "X-Custom-Header", "X-Another"},
			expectPreserved: []string{"X-Preserved"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripHopHeaders(tt.headers)

			for _, h := range tt.expectRemoved {
				if tt.headers.Get(h) != "" {
					t.Errorf("header %q should have been removed", h)
				}
			}

			for _, h := range tt.expectPreserved {
				if tt.headers.Get(h) == "" {
					t.Errorf("header %q should have been preserved", h)
				}
			}
		})
	}
}

func TestCopyHeaders(t *testing.T) {
	src := http.Header{
		"Content-Type":      []string{"application/json"},
		"X-Custom":          []string{"value1", "value2"},
		"Connection":        []string{"keep-alive"},
		"Transfer-Encoding": []string{"chunked"},
	}

	dst := http.Header{
		"X-Old-Header": []string{"old-value"},
	}

	copyHeaders(dst, src)

	// Should copy non-hop-by-hop headers
	if dst.Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be copied")
	}

	// Should copy multiple values
	xCustom := dst.Values("X-Custom")
	if len(xCustom) != 2 {
		t.Errorf("X-Custom should have 2 values, got %d", len(xCustom))
	}

	// Should not copy hop-by-hop headers
	if dst.Get("Connection") != "" {
		t.Error("Connection header should not be copied")
	}
	if dst.Get("Transfer-Encoding") != "" {
		t.Error("Transfer-Encoding header should not be copied")
	}

	// Should clear old headers from dst
	if dst.Get("X-Old-Header") != "" {
		t.Error("X-Old-Header should have been cleared")
	}
}

func TestIsCursorPath(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		// Valid cursor paths
		{"/_api/cursor", true},
		{"/_api/cursor/12345", true},
		{"/_db/mydb/_api/cursor", true},
		{"/_db/mydb/_api/cursor/12345", true},
		{"/_db/test_db/_api/cursor", true},
		{"/_db/my-db/_api/cursor/999", true},

		// Invalid cursor paths
		{"/_api/cursor/", false},          // trailing slash
		{"/_api/cursor/abc", false},       // non-numeric cursor ID
		{"/_api/cursorx", false},          // suffix
		{"/_api/document", false},         // different API
		{"/_db/../_api/cursor", false},    // path traversal attempt
		{"/_db/my.db/_api/cursor", false}, // invalid db name character
		{"/_db/my/db/_api/cursor", false}, // slash in db name
		{"/prefix/_api/cursor", false},    // invalid prefix
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := IsCursorPath(tt.path)
			if result != tt.expected {
				t.Errorf("IsCursorPath(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestHasAPIPathPrefix(t *testing.T) {
	tests := []struct {
		name     string
		fullPath string
		apiPath  string
		expected bool
	}{
		// Direct API paths
		{"direct document path", "/_api/document/collection/key", "/_api/document", true},
		{"direct collection path", "/_api/collection", "/_api/collection", true},
		{"direct index path", "/_api/index/collection/12345", "/_api/index", true},
		{"direct import path", "/_api/import", "/_api/import", true},

		// Database-prefixed paths
		{"db-prefixed document", "/_db/mydb/_api/document/coll", "/_api/document", true},
		{"db-prefixed collection", "/_db/test_db/_api/collection", "/_api/collection", true},
		{"db-prefixed with hyphen", "/_db/my-db/_api/index", "/_api/index", true},

		// Rejection cases
		{"wrong API path", "/_api/version", "/_api/document", false},
		{"partial match", "/_api/documentx", "/_api/document", false},
		{"path traversal attempt", "/_db/../_api/document", "/_api/document", false},
		{"embedded in path", "/foo/_api/document", "/_api/document", false},
		{"contains but not prefix", "/foo/_api/document/bar", "/_api/document", false},
		{"invalid db char dot", "/_db/my.db/_api/document", "/_api/document", false},
		{"invalid db char slash", "/_db/my/db/_api/document", "/_api/document", false},
		{"empty db name", "/_db//_api/document", "/_api/document", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasAPIPathPrefix(tt.fullPath, tt.apiPath)
			if result != tt.expected {
				t.Errorf("HasAPIPathPrefix(%q, %q) = %v, want %v",
					tt.fullPath, tt.apiPath, result, tt.expected)
			}
		})
	}
}

func TestGetEnv(t *testing.T) {
	// Test with set variable
	os.Setenv("TEST_VAR_SET", "custom_value")
	defer os.Unsetenv("TEST_VAR_SET")

	if got := GetEnv("TEST_VAR_SET", "default"); got != "custom_value" {
		t.Errorf("GetEnv() = %q, want %q", got, "custom_value")
	}

	// Test with unset variable
	if got := GetEnv("TEST_VAR_UNSET", "default"); got != "default" {
		t.Errorf("GetEnv() = %q, want %q", got, "default")
	}

	// Test with empty variable (should return fallback)
	os.Setenv("TEST_VAR_EMPTY", "")
	defer os.Unsetenv("TEST_VAR_EMPTY")

	if got := GetEnv("TEST_VAR_EMPTY", "fallback"); got != "fallback" {
		t.Errorf("GetEnv() with empty var = %q, want %q", got, "fallback")
	}
}

func TestBodyPeeker(t *testing.T) {
	// Create a mock request with body
	body := `{"query": "FOR doc IN collection RETURN doc"}`
	req := httptest.NewRequest(http.MethodPost, "/_api/cursor", strings.NewReader(body))

	// Create the proxy's internal body reader simulation
	var cachedBody []byte
	bodyConsumed := false

	bodyReader := func(limit int64) ([]byte, error) {
		if bodyConsumed {
			return cachedBody, nil
		}
		if req.Body == nil {
			bodyConsumed = true
			return nil, nil
		}
		defer func() {
			bodyConsumed = true
		}()

		effectiveLimit := limit
		if effectiveLimit <= 0 || effectiveLimit > MaxBodyPeekSize {
			effectiveLimit = MaxBodyPeekSize
		}

		var buf bytes.Buffer
		lr := &io.LimitedReader{R: req.Body, N: effectiveLimit + 1}
		if _, err := buf.ReadFrom(lr); err != nil {
			return nil, err
		}
		if lr.N <= 0 {
			return nil, io.ErrUnexpectedEOF
		}

		cachedBody = buf.Bytes()
		return cachedBody, nil
	}

	// First read
	data, err := bodyReader(1024)
	if err != nil {
		t.Fatalf("bodyReader() error = %v", err)
	}
	if string(data) != body {
		t.Errorf("bodyReader() = %q, want %q", string(data), body)
	}

	// Second read should return cached data
	data2, err := bodyReader(1024)
	if err != nil {
		t.Fatalf("second bodyReader() error = %v", err)
	}
	if string(data2) != body {
		t.Errorf("second bodyReader() = %q, want %q", string(data2), body)
	}
}

func TestBodyPeekerExceedsLimit(t *testing.T) {
	// Create a request with body larger than limit
	largeBody := strings.Repeat("x", 1000)
	req := httptest.NewRequest(http.MethodPost, "/_api/cursor", strings.NewReader(largeBody))

	var cachedBody []byte
	bodyConsumed := false

	bodyReader := func(limit int64) ([]byte, error) {
		if bodyConsumed {
			return cachedBody, nil
		}
		if req.Body == nil {
			bodyConsumed = true
			return nil, nil
		}
		defer func() {
			bodyConsumed = true
		}()

		effectiveLimit := limit
		if effectiveLimit <= 0 || effectiveLimit > MaxBodyPeekSize {
			effectiveLimit = MaxBodyPeekSize
		}

		var buf bytes.Buffer
		lr := &io.LimitedReader{R: req.Body, N: effectiveLimit + 1}
		if _, err := buf.ReadFrom(lr); err != nil {
			return nil, err
		}
		if lr.N <= 0 {
			return nil, io.ErrUnexpectedEOF
		}

		cachedBody = buf.Bytes()
		return cachedBody, nil
	}

	// Should fail with small limit
	_, err := bodyReader(100)
	if err == nil {
		t.Error("bodyReader() should have failed with small limit")
	}
}

func TestCloneHeader(t *testing.T) {
	original := http.Header{
		"Content-Type": []string{"application/json"},
		"X-Multi":      []string{"value1", "value2"},
	}

	cloned := cloneHeader(original)

	// Verify values are copied
	if cloned.Get("Content-Type") != "application/json" {
		t.Error("Content-Type not cloned correctly")
	}

	// Verify it's a deep copy
	original.Set("Content-Type", "text/plain")
	if cloned.Get("Content-Type") != "application/json" {
		t.Error("Clone is not independent of original")
	}

	// Verify multiple values
	if len(cloned.Values("X-Multi")) != 2 {
		t.Error("Multiple values not cloned correctly")
	}
}

func TestNewServerWithTimeouts(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	server := NewServerWithTimeouts(handler)

	if server.ReadTimeout != DefaultReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", server.ReadTimeout, DefaultReadTimeout)
	}
	if server.WriteTimeout != DefaultWriteTimeout {
		t.Errorf("WriteTimeout = %v, want %v", server.WriteTimeout, DefaultWriteTimeout)
	}
	if server.IdleTimeout != DefaultIdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", server.IdleTimeout, DefaultIdleTimeout)
	}
}
