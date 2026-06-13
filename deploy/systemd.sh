#!/bin/bash
set -euo pipefail
cat > /etc/systemd/system/vayupress.service << 'EOF'
[Unit]
Description=VayuPress Sovereign Publishing Runtime
After=network.target

[Service]
Type=simple
User=vayupress
Group=vayupress
ExecStart=/opt/vayupress/current/vayupress
Restart=always
RestartSec=5
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/vayupress /var/log/vayupress
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && echo "✅ systemd service installed"
