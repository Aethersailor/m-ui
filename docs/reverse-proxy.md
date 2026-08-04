# Safe panel access

m-ui listens on `0.0.0.0:2095` by default so a new installation is immediately
reachable at `http://SERVER_IP:2095/`. This also exposes the full management API
wherever that port is reachable. An SSH tunnel remains available after changing
the panel bind to `127.0.0.1` in System settings:

```sh
ssh -L 2095:127.0.0.1:2095 user@server
```

Open `http://127.0.0.1:2095/` locally.

## HTTPS prerequisites

Before using a reverse proxy, set the panel UI bind endpoint to loopback in the
System settings page, and edit `/etc/m-ui/config.toml` only for bootstrap
defaults:

```toml
[security]
cookie_secure = true
```

Then restart m-ui. Keep the active panel UI endpoint on `127.0.0.1:2095`. Do
not bind the panel directly to a public interface just because a reverse proxy
is present.

## Caddy

```caddyfile
panel.example.com {
    encode zstd gzip
    reverse_proxy 127.0.0.1:2095
}
```

Caddy can obtain and renew TLS certificates when DNS and ports are configured
by the operator. The m-ui installer does not install Caddy or change firewall
rules.

## Nginx

```nginx
server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name panel.example.com;

    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    client_max_body_size 1m;
    proxy_read_timeout 60s;
    proxy_send_timeout 60s;

    location / {
        proxy_pass http://127.0.0.1:2095;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        add_header Cache-Control "no-store" always;
    }
}
```

Validate the Nginx configuration before reload:

```sh
sudo nginx -t
sudo systemctl reload nginx
```

## Mihomo dashboard API

The Mihomo dashboard API is not m-ui's `/api/v1` API. The System settings page
has separate fields for:

- the m-ui panel UI listener;
- Mihomo's `external-controller` bind listener;
- m-ui's loopback-only Controller connection target;
- exact dashboard CORS origins.

To use a dashboard hosted on another origin, set an appropriate external
controller bind address (for example `0.0.0.0` or `::`), add the dashboard's
exact `https://...` origin, and restart Mihomo after the save completes. Keep
the m-ui connection target on `127.0.0.1` or `::1`. Prefer a VPN or a
TLS-terminating, access-controlled reverse proxy for the Mihomo API; the m-ui
installer does not configure either one.

## Additional controls

For an Internet-reachable panel, add an allowlist, VPN boundary, or independent
proxy authentication. Add login rate limiting at the proxy because m-ui v0.1
intentionally ignores forwarded client-address headers and sees the proxy as
the socket peer. Do not cache API responses. If the Mihomo dashboard API is
proxied, protect it independently and preserve its bearer-secret requirement.
