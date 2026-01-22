# Systemd Service Files

These service files provide production-ready systemd integration for the ArangoDB Unix socket proxies.

## Prerequisites

1. Create a dedicated service user:

```bash
sudo useradd -r -s /usr/sbin/nologin arango-proxy
```

2. Add the service user to the arangodb group (for socket access):

```bash
sudo usermod -a -G arangodb arango-proxy
```

3. Install the proxy binaries:

```bash
sudo cp bin/roproxy /usr/local/bin/
sudo cp bin/rwproxy /usr/local/bin/
sudo chmod 755 /usr/local/bin/roproxy /usr/local/bin/rwproxy
```

## Installation

```bash
# Copy service files
sudo cp *.service /etc/systemd/system/

# Reload systemd
sudo systemctl daemon-reload

# Enable and start services
sudo systemctl enable --now arango-roproxy
sudo systemctl enable --now arango-rwproxy
```

## Managing Services

```bash
# Check status
sudo systemctl status arango-roproxy
sudo systemctl status arango-rwproxy

# View logs
sudo journalctl -u arango-roproxy -f
sudo journalctl -u arango-rwproxy -f

# Restart
sudo systemctl restart arango-roproxy
sudo systemctl restart arango-rwproxy
```

## Socket Access

After starting the services, the sockets will be available at:

- **Read-only**: `/run/arango-proxy/readonly.sock`
- **Read-write**: `/run/arango-proxy/readwrite.sock`

To allow applications to access the read-only socket, add their service users to the `arango-proxy` group:

```bash
sudo usermod -a -G arango-proxy myapp-user
```

## Security Notes

The service files include comprehensive security hardening:

- `NoNewPrivileges`: Prevents privilege escalation
- `ProtectSystem=strict`: Read-only filesystem except explicit paths
- `PrivateTmp`: Isolated /tmp
- `RestrictAddressFamilies=AF_UNIX`: Only Unix sockets allowed
- `MemoryDenyWriteExecute`: Prevents code injection

## Customization

To customize the configuration, create an override file:

```bash
sudo systemctl edit arango-roproxy
```

Add your overrides:

```ini
[Service]
Environment=PROXY_CLIENT_TIMEOUT_SECONDS=300
```
