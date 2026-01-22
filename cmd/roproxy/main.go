// Command roproxy runs the read-only ArangoDB Unix socket proxy.
//
// The proxy listens on a Unix socket and forwards requests to ArangoDB,
// blocking any requests that would modify data.
//
// Environment variables:
//   - LISTEN_SOCKET: Path for the proxy socket (default: /run/arango-proxy/readonly.sock)
//   - UPSTREAM_SOCKET: Path to ArangoDB socket (default: /run/arangodb3/arangodb.sock)
//   - PROXY_CLIENT_TIMEOUT_SECONDS: HTTP client timeout (default: 120, 0 to disable)
//   - PROXY_DIAL_TIMEOUT_SECONDS: Socket dial timeout (default: 10)
package main

import (
	"log"

	proxy "github.com/toddwbucy/arango-unix-proxy"
)

func main() {
	if err := proxy.RunReadOnlyProxy(); err != nil {
		log.Fatal(err)
	}
}
