package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
)

// ForbiddenAQLKeywords are AQL keywords that indicate write operations.
// These are blocked in read-only mode.
var ForbiddenAQLKeywords = map[string]struct{}{
	"INSERT":   {},
	"UPDATE":   {},
	"UPSERT":   {},
	"REMOVE":   {},
	"REPLACE":  {},
	"TRUNCATE": {},
	"DROP":     {},
}

// forbiddenKeywordsList is used for fallback scanning when JSON parsing fails.
var forbiddenKeywordsList = []string{"INSERT", "UPDATE", "UPSERT", "REMOVE", "REPLACE", "TRUNCATE", "DROP"}

// RunReadOnlyProxy starts the read-only proxy server.
// It blocks until the server stops or encounters a fatal error.
func RunReadOnlyProxy() error {
	listenSocket := GetEnv("LISTEN_SOCKET", DefaultROListenSocket)
	upstreamSocket := GetEnv("UPSTREAM_SOCKET", DefaultUpstreamSocket)

	if err := EnsureParentDir(listenSocket); err != nil {
		return fmt.Errorf("failed to prepare directory for %s: %w", listenSocket, err)
	}
	RemoveIfExists(listenSocket)

	proxy := NewUnixReverseProxy(upstreamSocket, AllowReadOnly)

	listener, err := net.Listen("unix", listenSocket)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listenSocket, err)
	}
	EnsureSocketMode(listenSocket, ROSocketPermissions)

	server := NewServerWithTimeouts(LogRequests(proxy))

	log.Printf("Read-only proxy listening on %s -> %s", listenSocket, upstreamSocket)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("proxy server error: %w", err)
	}
	return nil
}

// AllowReadOnly is an AllowFunc that permits only read operations.
// It allows GET, HEAD, OPTIONS unconditionally, and POST requests to
// the cursor API only if they don't contain write-operation keywords.
// DELETE is allowed on cursor paths to permit cursor cleanup.
func AllowReadOnly(r *http.Request, peek BodyPeeker) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	case http.MethodPost:
		if IsCursorPath(r.URL.Path) {
			body, err := peek(128 * 1024)
			if err != nil {
				return err
			}
			// Reject ambiguous bodies with more than one top-level "query"
			// field. Go's encoding/json keeps the last duplicate key, but
			// ArangoDB may resolve duplicates differently; inspecting one
			// value while the upstream executes the other would bypass the
			// keyword scan below. Refuse rather than guess.
			if count, ok := countTopLevelQueryKeys(body); ok && count > 1 {
				return fmt.Errorf("ambiguous request: multiple %q fields in cursor body", "query")
			}
			var payload struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(body, &payload); err == nil && payload.Query != "" {
				upper := strings.ToUpper(payload.Query)
				tokens := strings.FieldsFunc(upper, func(r rune) bool {
					return r < 'A' || r > 'Z'
				})
				for _, token := range tokens {
					if _, forbidden := ForbiddenAQLKeywords[token]; forbidden {
						return fmt.Errorf("forbidden keyword %q detected in AQL", token)
					}
				}
				return nil
			}
			// Fallback: conservative scan of raw body
			upper := strings.ToUpper(string(body))
			for _, keyword := range forbiddenKeywordsList {
				if strings.Contains(upper, keyword) {
					return fmt.Errorf("forbidden keyword %q detected in request body", keyword)
				}
			}
			return nil
		}
	case http.MethodDelete:
		// DELETE on cursor paths is allowed for cursor cleanup
		if IsCursorPath(r.URL.Path) {
			return nil
		}
	}
	return fmt.Errorf("method %s not permitted on %s", r.Method, r.URL.Path)
}

// countTopLevelQueryKeys reports how many times the key "query" appears at the
// top level of the JSON object in body, and whether body is a JSON object at
// all. It walks the token stream so that nested "query" keys (e.g. inside
// bindVars or options) are not counted. ok is false when body is not a JSON
// object, in which case the caller's fallback scanning applies.
func countTopLevelQueryKeys(body []byte) (count int, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return 0, false
	}
	if delim, isDelim := tok.(json.Delim); !isDelim || delim != '{' {
		return 0, false
	}

	depth := 1        // we have consumed the opening '{'
	expectKey := true // first token inside the object is a key
	for depth > 0 {
		t, err := dec.Token()
		if err != nil {
			// Malformed/truncated JSON: let the caller's parser and fallback
			// scan decide; don't claim a definitive count.
			return 0, false
		}
		if delim, isDelim := t.(json.Delim); isDelim {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 1 {
					// A nested value just closed; next top-level token is a key.
					expectKey = true
				}
			}
			continue
		}
		if depth == 1 {
			if expectKey {
				if s, isStr := t.(string); isStr && s == "query" {
					count++
				}
			}
			expectKey = !expectKey
		}
	}
	return count, true
}
