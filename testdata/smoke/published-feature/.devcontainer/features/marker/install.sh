#!/bin/sh
set -e
# Record what the install saw. common-utils must already have created
# the remote user (installsAfter).
printf '%s|%s|%s|%s\n' "$GREETING" "$_REMOTE_USER" "$_REMOTE_USER_HOME" "$(id -u "$_REMOTE_USER")" \
	> /usr/local/share/dcb-smoke-install.txt
cat > /usr/local/share/dcb-smoke-entrypoint.sh <<'SCRIPT'
#!/bin/sh
echo started > /tmp/dcb-smoke-entrypoint.txt
SCRIPT
chmod +x /usr/local/share/dcb-smoke-entrypoint.sh
