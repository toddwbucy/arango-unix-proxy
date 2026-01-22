// Package proxy provides Unix socket reverse proxies for ArangoDB with
// configurable access control policies.
package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

const (
	// DefaultUpstreamSocket is the default path to ArangoDB's Unix socket.
	DefaultUpstreamSocket = "/run/arangodb3/arangodb.sock"

	// DefaultROListenSocket is the default path for the read-only proxy socket.
	DefaultROListenSocket = "/run/arango-proxy/readonly.sock"

	// DefaultRWListenSocket is the default path for the read-write proxy socket.
	DefaultRWListenSocket = "/run/arango-proxy/readwrite.sock"

	// ROSocketPermissions are the default permissions for read-only sockets.
	ROSocketPermissions = 0o640

	// RWSocketPermissions are the default permissions for read-write sockets.
	RWSocketPermissions = 0o600

	// MaxBodyPeekSize is the maximum number of bytes that can be read from a
	// request body for inspection. This prevents memory exhaustion attacks.
	MaxBodyPeekSize = 16 * 1024 * 1024 // 16 MB

	// DefaultReadTimeout is the maximum duration for reading the entire request.
	DefaultReadTimeout = 30 * time.Second

	// DefaultWriteTimeout is the maximum duration before timing out writes of the response.
	DefaultWriteTimeout = 120 * time.Second

	// DefaultIdleTimeout is the maximum amount of time to wait for the next request.
	DefaultIdleTimeout = 120 * time.Second
)

// cursorPathRegexp matches ArangoDB cursor API paths.
// Database names are restricted to alphanumeric characters, underscores, and hyphens.
var cursorPathRegexp = regexp.MustCompile(`^(/_db/[a-zA-Z0-9_-]+)?/_api/cursor(?:/[0-9]+)?$`)

var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// BodyPeeker is a function that reads up to limit bytes from the request body.
// It caches the result so subsequent calls return the same data.
type BodyPeeker func(limit int64) ([]byte, error)

// AllowFunc determines whether a request should be allowed through the proxy.
// It receives the HTTP request and a BodyPeeker for inspecting the request body.
type AllowFunc func(*http.Request, BodyPeeker) error

// UnixReverseProxy forwards HTTP requests to an upstream server exposed via Unix socket.
type UnixReverseProxy struct {
	upstreamSocket string
	allowFunc      AllowFunc
	client         *http.Client
}

// NewUnixReverseProxy creates a new reverse proxy that forwards requests to the
// upstream Unix socket, applying the given allow function to each request.
func NewUnixReverseProxy(upstreamSocket string, allowFunc AllowFunc) *UnixReverseProxy {
	transport := newUnixTransport(upstreamSocket)
	timeoutSec := GetEnv("PROXY_CLIENT_TIMEOUT_SECONDS", GetEnv("CLIENT_TIMEOUT_SECONDS", "120"))
	timeout := 120 * time.Second
	if d, err := time.ParseDuration(timeoutSec + "s"); err == nil {
		timeout = d
	}
	if timeoutSec == "0" {
		log.Printf("proxy client timeout: disabled (0s)")
		return &UnixReverseProxy{
			upstreamSocket: upstreamSocket,
			allowFunc:      allowFunc,
			client: &http.Client{
				Transport: transport,
				Timeout:   0,
			},
		}
	}
	log.Printf("proxy client timeout: %s", timeout)
	return &UnixReverseProxy{
		upstreamSocket: upstreamSocket,
		allowFunc:      allowFunc,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
	}
}

func newUnixTransport(socketPath string) *http.Transport {
	dialTimeoutSec := GetEnv("PROXY_DIAL_TIMEOUT_SECONDS", "10")
	dialTimeout := 10 * time.Second
	if d, err := time.ParseDuration(dialTimeoutSec + "s"); err == nil {
		dialTimeout = d
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		ForceAttemptHTTP2: true,
	}
	if err := http2.ConfigureTransport(transport); err != nil {
		log.Printf("warning: failed to configure HTTP/2 transport, falling back to HTTP/1.1: %v", err)
	}
	return transport
}

// ServeHTTP implements the http.Handler interface.
func (p *UnixReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var cachedBody []byte
	bodyConsumed := false

	bodyReader := func(limit int64) ([]byte, error) {
		if bodyConsumed {
			return cachedBody, nil
		}
		if r.Body == nil {
			bodyConsumed = true
			return nil, nil
		}
		defer func() {
			bodyConsumed = true
		}()

		// Enforce maximum body size limit to prevent memory exhaustion
		effectiveLimit := limit
		if effectiveLimit <= 0 || effectiveLimit > MaxBodyPeekSize {
			effectiveLimit = MaxBodyPeekSize
		}

		var buf bytes.Buffer
		lr := &io.LimitedReader{R: r.Body, N: effectiveLimit + 1}
		if _, err := buf.ReadFrom(lr); err != nil {
			_ = r.Body.Close()
			return nil, err
		}
		if lr.N <= 0 {
			_ = r.Body.Close()
			return nil, fmt.Errorf("request body exceeds inspection limit (%d bytes)", effectiveLimit)
		}

		if err := r.Body.Close(); err != nil {
			log.Printf("warning: failed to close request body: %v", err)
		}
		cachedBody = append([]byte(nil), buf.Bytes()...)
		return cachedBody, nil
	}

	if err := p.allowFunc(r, bodyReader); err != nil {
		// Ensure body is closed on early return to prevent resource leaks
		if r.Body != nil && !bodyConsumed {
			_ = r.Body.Close()
		}
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	var upstreamBody io.ReadCloser
	if bodyConsumed {
		upstreamBody = io.NopCloser(bytes.NewReader(cachedBody))
	} else {
		upstreamBody = r.Body
	}

	upstreamURL := buildUpstreamURL(r)
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, upstreamBody)
	if err != nil {
		http.Error(w, "failed to build upstream request", http.StatusInternalServerError)
		return
	}

	copyHeaders(upstreamReq.Header, r.Header)
	if bodyConsumed {
		upstreamReq.ContentLength = int64(len(cachedBody))
	}

	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("warning: failed to copy upstream response: %v", err)
	}
}

func copyHeaders(dst, src http.Header) {
	cleaned := cloneHeader(src)
	stripHopHeaders(cleaned)

	for key := range dst {
		dst.Del(key)
	}

	for key, values := range cleaned {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func stripHopHeaders(header http.Header) {
	connectionValues := header.Values("Connection")
	for _, value := range connectionValues {
		for _, token := range strings.Split(value, ",") {
			headerName := strings.TrimSpace(token)
			if headerName == "" {
				continue
			}
			header.Del(http.CanonicalHeaderKey(headerName))
		}
	}

	for _, hopHeader := range hopByHopHeaders {
		header.Del(hopHeader)
	}
}

func cloneHeader(src http.Header) http.Header {
	cloned := make(http.Header, len(src))
	for key, values := range src {
		copied := make([]string, len(values))
		copy(copied, values)
		cloned[key] = copied
	}
	return cloned
}

func buildUpstreamURL(r *http.Request) string {
	var builder strings.Builder
	builder.WriteString("http://arangodb")
	builder.WriteString(r.URL.Path)
	if raw := r.URL.RawQuery; raw != "" {
		builder.WriteByte('?')
		builder.WriteString(raw)
	}
	return builder.String()
}

// RemoveIfExists removes a file if it exists, fatally logging on other errors.
func RemoveIfExists(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Fatalf("failed to remove existing socket %s: %v", path, err)
	}
}

// EnsureParentDir creates parent directories for the given path if needed.
func EnsureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" {
		return nil
	}
	return os.MkdirAll(dir, 0o750)
}

// EnsureSocketMode sets the permissions on a socket file.
func EnsureSocketMode(path string, mode os.FileMode) {
	if err := os.Chmod(path, mode); err != nil {
		log.Fatalf("failed to chmod %s: %v", path, err)
	}
}

// GetEnv returns the value of an environment variable or a fallback default.
func GetEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// LogRequests wraps an http.Handler to log each request's method and path.
func LogRequests(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loggedPath := r.URL.Path
		if r.URL.RawQuery != "" {
			loggedPath += "?<redacted>"
		}
		log.Printf("%s %s", r.Method, loggedPath)
		handler.ServeHTTP(w, r)
	})
}

// IsCursorPath returns true if the path matches the ArangoDB cursor API pattern.
func IsCursorPath(path string) bool {
	return cursorPathRegexp.MatchString(path)
}

// NewServerWithTimeouts creates an HTTP server with sensible timeout defaults.
func NewServerWithTimeouts(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:      handler,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: DefaultWriteTimeout,
		IdleTimeout:  DefaultIdleTimeout,
	}
}

// apiPathRegexp matches ArangoDB API paths with optional database prefix.
// Used for validating paths like /_api/document or /_db/mydb/_api/document.
var apiPathRegexp = regexp.MustCompile(`^(/_db/[a-zA-Z0-9_-]+)?/_api/`)

// HasAPIPathPrefix checks if the path has the given API path prefix.
// It properly handles paths with optional database prefix (/_db/name/).
// This should be used instead of strings.Contains to prevent path traversal attacks.
func HasAPIPathPrefix(fullPath, apiPath string) bool {
	// Normalize the path to prevent directory traversal
	cleanPath := filepath.Clean(fullPath)
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}

	// Reject if the cleaned path differs from original in a way that suggests traversal
	// (filepath.Clean removes .. components)
	if strings.Contains(fullPath, "..") {
		return false
	}

	// Check for exact prefix match with path boundary
	// The API path must be followed by / or end of string or query params
	if matchesAPIPath(cleanPath, apiPath) {
		return true
	}

	// Database-prefixed match: /_db/name/_api/document/...
	// First check if it starts with /_db/
	if strings.HasPrefix(cleanPath, "/_db/") {
		// Find the end of the database name
		rest := cleanPath[5:] // Skip "/_db/"
		slashIdx := strings.Index(rest, "/")
		if slashIdx > 0 {
			// Validate database name (alphanumeric, underscore, hyphen only)
			dbName := rest[:slashIdx]
			validDB := true
			for _, c := range dbName {
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
					validDB = false
					break
				}
			}
			if validDB {
				// Check if the rest of the path starts with the API path
				pathAfterDB := rest[slashIdx:]
				if matchesAPIPath(pathAfterDB, apiPath) {
					return true
				}
			}
		}
	}

	return false
}

// matchesAPIPath checks if path starts with apiPath at a path boundary.
// This prevents /_api/documentx from matching /_api/document.
func matchesAPIPath(path, apiPath string) bool {
	if !strings.HasPrefix(path, apiPath) {
		return false
	}
	// Must be exact match or followed by / or ?
	if len(path) == len(apiPath) {
		return true
	}
	nextChar := path[len(apiPath)]
	return nextChar == '/' || nextChar == '?'
}
