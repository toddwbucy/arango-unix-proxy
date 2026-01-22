package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockBodyPeeker creates a BodyPeeker that returns the given body content.
func mockBodyPeeker(body string) BodyPeeker {
	return func(limit int64) ([]byte, error) {
		return []byte(body), nil
	}
}

// emptyBodyPeeker returns a BodyPeeker that returns empty content.
func emptyBodyPeeker() BodyPeeker {
	return func(limit int64) ([]byte, error) {
		return nil, nil
	}
}

func TestAllowReadOnly_GET(t *testing.T) {
	paths := []string{
		"/_api/version",
		"/_api/cursor/12345",
		"/_api/document/collection/key",
		"/_db/mydb/_api/collection",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			err := AllowReadOnly(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("GET %s should be allowed, got error: %v", path, err)
			}
		})
	}
}

func TestAllowReadOnly_HEAD(t *testing.T) {
	req := httptest.NewRequest(http.MethodHead, "/_api/version", nil)
	err := AllowReadOnly(req, emptyBodyPeeker())
	if err != nil {
		t.Errorf("HEAD should be allowed, got error: %v", err)
	}
}

func TestAllowReadOnly_OPTIONS(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/_api/version", nil)
	err := AllowReadOnly(req, emptyBodyPeeker())
	if err != nil {
		t.Errorf("OPTIONS should be allowed, got error: %v", err)
	}
}

func TestAllowReadOnly_POST_Cursor_ReadQuery(t *testing.T) {
	queries := []string{
		`{"query": "FOR doc IN collection RETURN doc"}`,
		`{"query": "FOR doc IN collection FILTER doc.name == 'test' RETURN doc"}`,
		`{"query": "RETURN 1 + 1"}`,
		`{"query": "FOR i IN 1..10 RETURN i"}`,
		`{"query": "LET x = (FOR doc IN coll RETURN doc) RETURN x"}`,
	}

	for _, body := range queries {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/_api/cursor", nil)
			err := AllowReadOnly(req, mockBodyPeeker(body))
			if err != nil {
				t.Errorf("POST cursor with read query should be allowed, got error: %v", err)
			}
		})
	}
}

func TestAllowReadOnly_POST_Cursor_WriteQuery(t *testing.T) {
	queries := []struct {
		body    string
		keyword string
	}{
		{`{"query": "INSERT {name: 'test'} INTO collection"}`, "INSERT"},
		{`{"query": "FOR doc IN collection UPDATE doc WITH {x: 1} IN collection"}`, "UPDATE"},
		{`{"query": "UPSERT {_key: '1'} INSERT {} UPDATE {} IN collection"}`, "UPSERT"},
		{`{"query": "FOR doc IN collection REMOVE doc IN collection"}`, "REMOVE"},
		{`{"query": "REPLACE {_key: '1'} WITH {x: 1} IN collection"}`, "REPLACE"},
	}

	for _, tc := range queries {
		t.Run(tc.keyword, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/_api/cursor", nil)
			err := AllowReadOnly(req, mockBodyPeeker(tc.body))
			if err == nil {
				t.Errorf("POST cursor with %s should be blocked", tc.keyword)
			}
			if !strings.Contains(err.Error(), tc.keyword) {
				t.Errorf("error should mention %s, got: %v", tc.keyword, err)
			}
		})
	}
}

func TestAllowReadOnly_POST_Cursor_AllKeywords(t *testing.T) {
	// Test all forbidden keywords
	for keyword := range ForbiddenAQLKeywords {
		t.Run(keyword, func(t *testing.T) {
			body := `{"query": "` + keyword + ` something"}`
			req := httptest.NewRequest(http.MethodPost, "/_api/cursor", nil)
			err := AllowReadOnly(req, mockBodyPeeker(body))
			if err == nil {
				t.Errorf("keyword %s should be blocked", keyword)
			}
		})
	}
}

func TestAllowReadOnly_POST_Cursor_CaseInsensitive(t *testing.T) {
	variations := []string{
		`{"query": "insert {x:1} INTO coll"}`,
		`{"query": "INSERT {x:1} INTO coll"}`,
		`{"query": "Insert {x:1} Into coll"}`,
		`{"query": "iNsErT {x:1} INTO coll"}`,
	}

	for _, body := range variations {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/_api/cursor", nil)
			err := AllowReadOnly(req, mockBodyPeeker(body))
			if err == nil {
				t.Error("INSERT should be blocked regardless of case")
			}
		})
	}
}

func TestAllowReadOnly_POST_Cursor_FallbackScanning(t *testing.T) {
	// Test with malformed JSON that contains keywords
	bodies := []string{
		`not valid json but contains INSERT`,
		`{"broken: "INSERT INTO collection"}`,
	}

	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/_api/cursor", nil)
			err := AllowReadOnly(req, mockBodyPeeker(body))
			if err == nil {
				t.Error("malformed body with INSERT should be blocked")
			}
		})
	}
}

func TestAllowReadOnly_PUT_Cursor_Blocked(t *testing.T) {
	// PUT on cursor paths should NOT be allowed (security fix)
	req := httptest.NewRequest(http.MethodPut, "/_api/cursor/12345", nil)
	err := AllowReadOnly(req, emptyBodyPeeker())
	if err == nil {
		t.Error("PUT on cursor should be blocked in read-only mode")
	}
}

func TestAllowReadOnly_DELETE_Cursor(t *testing.T) {
	// DELETE on cursor paths IS allowed (cursor cleanup)
	paths := []string{
		"/_api/cursor/12345",
		"/_db/mydb/_api/cursor/67890",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, path, nil)
			err := AllowReadOnly(req, emptyBodyPeeker())
			if err != nil {
				t.Errorf("DELETE cursor should be allowed, got error: %v", err)
			}
		})
	}
}

func TestAllowReadOnly_POST_NonCursor(t *testing.T) {
	// POST to non-cursor paths should be blocked
	paths := []string{
		"/_api/document/collection",
		"/_api/collection",
		"/_api/index",
		"/_api/import",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			err := AllowReadOnly(req, emptyBodyPeeker())
			if err == nil {
				t.Errorf("POST %s should be blocked in read-only mode", path)
			}
		})
	}
}

func TestAllowReadOnly_DELETE_NonCursor(t *testing.T) {
	// DELETE to non-cursor paths should be blocked
	paths := []string{
		"/_api/document/collection/key",
		"/_api/collection/myCollection",
		"/_api/index/collection/12345",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, path, nil)
			err := AllowReadOnly(req, emptyBodyPeeker())
			if err == nil {
				t.Errorf("DELETE %s should be blocked in read-only mode", path)
			}
		})
	}
}

func TestAllowReadOnly_PUT_NonCursor(t *testing.T) {
	// PUT to any path should be blocked
	paths := []string{
		"/_api/document/collection/key",
		"/_api/collection/myCollection",
		"/_api/version",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, path, nil)
			err := AllowReadOnly(req, emptyBodyPeeker())
			if err == nil {
				t.Errorf("PUT %s should be blocked in read-only mode", path)
			}
		})
	}
}

func TestAllowReadOnly_PATCH(t *testing.T) {
	// PATCH should always be blocked in read-only mode
	req := httptest.NewRequest(http.MethodPatch, "/_api/document/coll/key", nil)
	err := AllowReadOnly(req, emptyBodyPeeker())
	if err == nil {
		t.Error("PATCH should be blocked in read-only mode")
	}
}

func TestAllowReadOnly_KeywordInIdentifier(t *testing.T) {
	// Keywords embedded in identifiers should NOT trigger blocking
	// e.g., "updatedAt" contains "UPDATE" but is not the UPDATE keyword
	queries := []string{
		`{"query": "FOR doc IN collection RETURN doc.updatedAt"}`,
		`{"query": "FOR doc IN collection FILTER doc.insertTime > 0 RETURN doc"}`,
		`{"query": "FOR doc IN collection RETURN doc.removeFlag"}`,
	}

	for _, body := range queries {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/_api/cursor", nil)
			err := AllowReadOnly(req, mockBodyPeeker(body))
			if err != nil {
				t.Errorf("identifier containing keyword substring should be allowed, got: %v", err)
			}
		})
	}
}

func TestAllowReadOnly_DatabasePrefix(t *testing.T) {
	// Test cursor operations with database prefix
	t.Run("POST cursor with db prefix", func(t *testing.T) {
		body := `{"query": "FOR doc IN collection RETURN doc"}`
		req := httptest.NewRequest(http.MethodPost, "/_db/mydb/_api/cursor", nil)
		err := AllowReadOnly(req, mockBodyPeeker(body))
		if err != nil {
			t.Errorf("POST cursor with db prefix should be allowed: %v", err)
		}
	})

	t.Run("DELETE cursor with db prefix", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/_db/mydb/_api/cursor/12345", nil)
		err := AllowReadOnly(req, emptyBodyPeeker())
		if err != nil {
			t.Errorf("DELETE cursor with db prefix should be allowed: %v", err)
		}
	})
}

func TestForbiddenAQLKeywords(t *testing.T) {
	// Verify the exported map contains all expected keywords
	expected := []string{"INSERT", "UPDATE", "UPSERT", "REMOVE", "REPLACE", "TRUNCATE", "DROP"}

	for _, kw := range expected {
		if _, ok := ForbiddenAQLKeywords[kw]; !ok {
			t.Errorf("ForbiddenAQLKeywords should contain %s", kw)
		}
	}

	if len(ForbiddenAQLKeywords) != len(expected) {
		t.Errorf("ForbiddenAQLKeywords has %d entries, expected %d",
			len(ForbiddenAQLKeywords), len(expected))
	}
}
