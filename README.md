# arango-unix-proxy

Unix socket reverse proxy for ArangoDB with configurable access control policies.

## Overview

This project provides two proxy binaries that sit between clients and ArangoDB, enforcing access control through Unix socket permissions and request filtering:

- **roproxy** (Read-Only Proxy): Allows only read operations
- **rwproxy** (Read-Write Proxy): Allows read operations plus document CRUD, imports, and collection/index management

## Installation

```bash
go install github.com/toddwbucy/arango-unix-proxy/cmd/roproxy@latest
go install github.com/toddwbucy/arango-unix-proxy/cmd/rwproxy@latest
```

Or build from source:

```bash
git clone https://github.com/toddwbucy/arango-unix-proxy.git
cd arango-unix-proxy
go build -o bin/roproxy ./cmd/roproxy
go build -o bin/rwproxy ./cmd/rwproxy
```

## Configuration

Both proxies are configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_SOCKET` | `/run/arango-proxy/readonly.sock` (ro) or `/run/arango-proxy/readwrite.sock` (rw) | Path for the proxy socket |
| `UPSTREAM_SOCKET` | `/run/arangodb3/arangodb.sock` | Path to ArangoDB's Unix socket |
| `PROXY_CLIENT_TIMEOUT_SECONDS` | `120` | HTTP client timeout (0 to disable) |
| `PROXY_DIAL_TIMEOUT_SECONDS` | `10` | Socket dial timeout |

## Security Model

### Read-Only Proxy (roproxy)

The read-only proxy permits:

- **GET, HEAD, OPTIONS**: Always allowed
- **POST to cursor API**: Allowed only if the AQL query contains no write keywords
- **DELETE to cursor API**: Allowed for cursor cleanup

Blocked AQL keywords: `INSERT`, `UPDATE`, `UPSERT`, `REMOVE`, `REPLACE`, `TRUNCATE`, `DROP`

Socket permissions: `0640` (owner read/write, group read)

### Read-Write Proxy (rwproxy)

The read-write proxy permits all read-only operations plus:

- **POST/PUT/PATCH/DELETE** on `/_api/document`
- **POST/PUT/PATCH/DELETE** on `/_api/collection`
- **POST/PUT/PATCH/DELETE** on `/_api/index`
- **POST** on `/_api/import`
- **POST** on cursor API (all queries, including write operations)

Socket permissions: `0600` (owner read/write only)

### Path Security

All API paths support the optional database prefix format: `/_db/<database>/_api/...`

Database names are validated to contain only alphanumeric characters, underscores, and hyphens to prevent path traversal attacks.

## Deployment

See `examples/systemd/` for production systemd service files with security hardening.

### Basic Usage

```bash
# Start read-only proxy
UPSTREAM_SOCKET=/var/run/arangodb3/arangodb.sock \
LISTEN_SOCKET=/var/run/arango-proxy/readonly.sock \
./bin/roproxy

# Start read-write proxy
UPSTREAM_SOCKET=/var/run/arangodb3/arangodb.sock \
LISTEN_SOCKET=/var/run/arango-proxy/readwrite.sock \
./bin/rwproxy
```

### Client Connection

Clients connect via the proxy socket instead of directly to ArangoDB:

```bash
# Using curl with Unix socket
curl --unix-socket /var/run/arango-proxy/readonly.sock \
  http://localhost/_api/version

# Using arangosh
arangosh --server.protocol unix \
  --server.endpoint unix:///var/run/arango-proxy/readonly.sock
```

## Architecture

```
                    +-----------------+
  Client --------> | Read-Only Proxy | ------+
 (group member)    | (0640 socket)   |       |
                   +-----------------+       |      +------------+
                                             +----> | ArangoDB   |
                   +-----------------+       |      | (upstream) |
  Client --------> | Read-Write Proxy| ------+      +------------+
 (owner only)      | (0600 socket)   |
                   +-----------------+
```

## Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests with verbose output
go test -v ./...
```

## License

MIT License - see [LICENSE](LICENSE) for details.
