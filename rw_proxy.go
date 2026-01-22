package proxy

import (
	"fmt"
	"log"
	"net"
	"net/http"
)

// AllowedRWAPIPaths are the API paths that the read-write proxy allows
// for write operations (POST, PUT, PATCH, DELETE).
var AllowedRWAPIPaths = []string{
	"/_api/document",
	"/_api/collection",
	"/_api/index",
	"/_api/import",
}

// RunReadWriteProxy starts the read-write proxy server.
// It blocks until the server stops or encounters a fatal error.
func RunReadWriteProxy() error {
	listenSocket := GetEnv("LISTEN_SOCKET", DefaultRWListenSocket)
	upstreamSocket := GetEnv("UPSTREAM_SOCKET", DefaultUpstreamSocket)

	if err := EnsureParentDir(listenSocket); err != nil {
		return fmt.Errorf("failed to prepare directory for %s: %w", listenSocket, err)
	}
	RemoveIfExists(listenSocket)

	proxy := NewUnixReverseProxy(upstreamSocket, AllowReadWrite)

	listener, err := net.Listen("unix", listenSocket)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listenSocket, err)
	}
	EnsureSocketMode(listenSocket, RWSocketPermissions)

	server := NewServerWithTimeouts(LogRequests(proxy))

	log.Printf("Read-write proxy listening on %s -> %s", listenSocket, upstreamSocket)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("proxy server error: %w", err)
	}
	return nil
}

// AllowReadWrite is an AllowFunc that permits read and write operations.
// It allows all read-only operations plus document CRUD, import, collection,
// and index management operations.
func AllowReadWrite(r *http.Request, peek BodyPeeker) error {
	// First check if the read-only policy allows it
	if err := AllowReadOnly(r, peek); err == nil {
		return nil
	}

	path := r.URL.Path

	switch r.Method {
	case http.MethodPost:
		// POST is allowed on cursor paths (for AQL queries that may write)
		// and on document/collection/index/import paths
		if IsCursorPath(path) {
			return nil
		}
		for _, apiPath := range AllowedRWAPIPaths {
			if HasAPIPathPrefix(path, apiPath) {
				return nil
			}
		}
	case http.MethodPut, http.MethodPatch, http.MethodDelete:
		// PUT/PATCH/DELETE are allowed on document/collection/index paths
		for _, apiPath := range []string{"/_api/document", "/_api/collection", "/_api/index"} {
			if HasAPIPathPrefix(path, apiPath) {
				return nil
			}
		}
	}

	return fmt.Errorf("method %s not permitted on %s", r.Method, path)
}
