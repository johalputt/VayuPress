#!/bin/bash
set -euo pipefail
DOMAIN="${DOMAIN:-localhost}"
UPSTREAM="${UPSTREAM:-127.0.0.1:8080}"
cat > /etc/nginx/sites-available/vayupress << EOF
limit_req_zone \$binary_remote_addr zone=vp_api:10m rate=30r/m;
limit_req_zone \$binary_remote_addr zone=vp_global:10m rate=60r/m;
server {
    listen 443 ssl http2;
    server_name $DOMAIN;
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options SAMEORIGIN always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Content-Security-Policy "default-src 'self'" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;
    add_header Permissions-Policy "geolocation=(), camera=(), microphone=()" always;
    location /api/ { limit_req zone=vp_api burst=10 nodelay; proxy_pass http://$UPSTREAM; proxy_pass_header X-CSRF-Token; }
    location / { limit_req zone=vp_global burst=20 nodelay; proxy_pass http://$UPSTREAM; }
}
EOF
nginx -t && echo "✅ nginx config OK"
