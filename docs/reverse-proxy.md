# Safe panel access

m-ui listens on `127.0.0.1:2095` by default. The simplest remote access method
is an SSH tunnel:

```sh
ssh -L 2095:127.0.0.1:2095 user@server
```

Open `http://127.0.0.1:2095/` locally.

## HTTPS prerequisites

Before using a reverse proxy, edit `/etc/m-ui/config.toml`:

```toml
[security]
cookie_secure = true
```

Then restart m-ui. Keep `server.listen_address = "127.0.0.1"`. Do not bind the
panel directly to a public interface just because a reverse proxy is present.

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

## Additional controls

For an Internet-reachable panel, add an allowlist, VPN boundary, or independent
proxy authentication. Add login rate limiting at the proxy because m-ui v0.1
intentionally ignores forwarded client-address headers and sees the proxy as
the socket peer. Do not cache API responses. Never proxy the Mihomo Controller
port `9090`; it is an internal authenticated loopback API.
