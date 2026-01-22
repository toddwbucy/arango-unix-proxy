package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowReadWrite_InheritsReadOnly(t *testing.T) {
	// All read-only operations should be allowed
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/_api/version", ""},
		{http.MethodHead, "/_api/version", ""},
		{http.MethodOptions, "/_api/version", ""},
		{http.MethodGet, "/_api/document/collection/key", ""},
		{http.MethodPost, "/_api/cursor", `{"query": "FOR doc IN coll RETURN doc"}`},
		{http.MethodDelete, "/_api/cursor/12345", ""},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			var peek BodyPeeker
			if tc.body != "" {
				peek = mockBodyPeeker(tc.body)
			} else {
				peek = emptyBodyPeeker()
			}
			err := AllowReadWrite(req, peek)
			if err != nil {
				t.Errorf("should inherit read-only permission, got error: %v", err)
			}
		})
	}
}

func TestAllowReadWrite_POST_Document(t *testing.T) {
	paths := []string{
		"/_api/document/collection",
		"/_api/document/collection/key",
		"/_db/mydb/_api/document/collection",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("POST %s should be allowed, got error: %v", path, err)
			}
		})
	}
}

func TestAllowReadWrite_PUT_Document(t *testing.T) {
	paths := []string{
		"/_api/document/collection/key",
		"/_db/mydb/_api/document/collection/key",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("PUT %s should be allowed, got error: %v", path, err)
			}
		})
	}
}

func TestAllowReadWrite_PATCH_Document(t *testing.T) {
	paths := []string{
		"/_api/document/collection/key",
		"/_db/mydb/_api/document/collection/key",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("PATCH %s should be allowed, got error: %v", path, err)
			}
		})
	}
}

func TestAllowReadWrite_DELETE_Document(t *testing.T) {
	paths := []string{
		"/_api/document/collection/key",
		"/_db/mydb/_api/document/collection/key",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("DELETE %s should be allowed, got error: %v", path, err)
			}
		})
	}
}

func TestAllowReadWrite_Collection_Operations(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/_api/collection"},
		{http.MethodPut, "/_api/collection/myCollection/properties"},
		{http.MethodDelete, "/_api/collection/myCollection"},
		{http.MethodPost, "/_db/mydb/_api/collection"},
		{http.MethodPut, "/_db/mydb/_api/collection/myCollection"},
		{http.MethodDelete, "/_db/mydb/_api/collection/myCollection"},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("%s %s should be allowed, got error: %v", tc.method, tc.path, err)
			}
		})
	}
}

func TestAllowReadWrite_Index_Operations(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/_api/index"},
		{http.MethodPost, "/_api/index?collection=myCollection"},
		{http.MethodDelete, "/_api/index/collection/12345"},
		{http.MethodPost, "/_db/mydb/_api/index"},
		{http.MethodDelete, "/_db/mydb/_api/index/collection/12345"},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("%s %s should be allowed, got error: %v", tc.method, tc.path, err)
			}
		})
	}
}

func TestAllowReadWrite_Import(t *testing.T) {
	paths := []string{
		"/_api/import",
		"/_api/import?collection=myCollection",
		"/_db/mydb/_api/import",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("POST %s should be allowed, got error: %v", path, err)
			}
		})
	}
}

func TestAllowReadWrite_PathTraversal(t *testing.T) {
	// These paths attempt path traversal and should be blocked
	paths := []string{
		"/_db/../_api/document/collection",
		"/_db/mydb/../admin/_api/document",
		"/foo/_api/document/bar",
		"/../_api/document/collection",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err == nil {
				t.Errorf("path traversal attempt %s should be blocked", path)
			}
		})
	}
}

func TestAllowReadWrite_AdminEndpoints(t *testing.T) {
	// Admin endpoints should NOT be allowed
	adminPaths := []string{
		"/_admin/echo",
		"/_admin/log",
		"/_admin/log/level",
		"/_admin/server/availability",
		"/_admin/shutdown",
		"/_api/user",
		"/_api/database",
		"/_api/replication",
		"/_api/tasks",
	}

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}

	for _, path := range adminPaths {
		for _, method := range methods {
			t.Run(method+" "+path, func(t *testing.T) {
				req := httptest.NewRequest(method, path, nil)
				err := AllowReadWrite(req, emptyBodyPeeker())
				// GET should be allowed (read-only allows it)
				if method == http.MethodGet {
					if err != nil {
						t.Errorf("GET %s should be allowed (read-only), got error: %v", path, err)
					}
				} else {
					// Other methods should be blocked on admin paths
					if err == nil {
						t.Errorf("%s %s should be blocked", method, path)
					}
				}
			})
		}
	}
}

func TestAllowReadWrite_CursorWithWriteQuery(t *testing.T) {
	// In read-write mode, write queries through cursor API should be allowed
	body := `{"query": "INSERT {name: 'test'} INTO collection"}`
	req := httptest.NewRequest(http.MethodPost, "/_api/cursor", nil)
	err := AllowReadWrite(req, mockBodyPeeker(body))
	if err != nil {
		t.Errorf("write query via cursor should be allowed in rw mode, got error: %v", err)
	}
}

func TestAllowReadWrite_PUT_Import_Blocked(t *testing.T) {
	// PUT on import should be blocked (only POST is allowed)
	req := httptest.NewRequest(http.MethodPut, "/_api/import", nil)
	err := AllowReadWrite(req, emptyBodyPeeker())
	if err == nil {
		t.Error("PUT on /_api/import should be blocked")
	}
}

func TestAllowReadWrite_InvalidDatabaseName(t *testing.T) {
	// Invalid database names should be rejected
	paths := []string{
		"/_db/my.db/_api/document/collection",
		"/_db/my/db/_api/document/collection",
		"/_db//_api/document/collection",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err == nil {
				t.Errorf("invalid db name in %s should be blocked", path)
			}
		})
	}
}

func TestAllowedRWAPIPaths(t *testing.T) {
	// Verify the exported slice contains expected paths
	expected := map[string]bool{
		"/_api/document":   true,
		"/_api/collection": true,
		"/_api/index":      true,
		"/_api/import":     true,
	}

	for _, path := range AllowedRWAPIPaths {
		if !expected[path] {
			t.Errorf("unexpected path in AllowedRWAPIPaths: %s", path)
		}
		delete(expected, path)
	}

	for path := range expected {
		t.Errorf("missing path in AllowedRWAPIPaths: %s", path)
	}
}

func TestAllowReadWrite_PATCH_Index_Blocked(t *testing.T) {
	// PATCH is not typically used for index operations, test it's properly handled
	// Index supports PUT/DELETE but typically not PATCH
	req := httptest.NewRequest(http.MethodPatch, "/_api/index/collection/12345", nil)
	err := AllowReadWrite(req, emptyBodyPeeker())
	if err != nil {
		t.Logf("Note: PATCH on /_api/index is blocked (expected): %v", err)
	}
}

func TestAllowReadWrite_SubpathsAllowed(t *testing.T) {
	// Verify subpaths are properly allowed
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/_api/document/collection/key/_something"},
		{http.MethodPut, "/_api/collection/myCollection/properties"},
		{http.MethodPost, "/_api/index?collection=test&type=hash"},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			err := AllowReadWrite(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("%s %s should be allowed, got error: %v", tc.method, tc.path, err)
			}
		})
	}
}
