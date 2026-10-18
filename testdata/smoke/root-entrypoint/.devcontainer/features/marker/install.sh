#!/bin/sh
# relationships:
#   references: devcontainer-feature
set -eu
id sample-user >/dev/null 2>&1 || adduser -D -u 1234 sample-user
cat > /usr/local/bin/sample-start <<'SCRIPT'
#!/bin/sh
set -eu
# Docker-in-Docker style entrypoints need root before remote commands start.
test "$(id -u)" = 0
id -u > /root/sample-start-user
cp /root/sample-start-user /tmp/entrypoint-user
SCRIPT
chmod +x /usr/local/bin/sample-start
