#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Refuse an unescaped backtick inside an UNQUOTED heredoc body.
#
# These scripts write nginx, systemd and env files from heredocs. With an
# unquoted delimiter (`cat > f <<NGINX`) the body is expanded — which is the
# point, the config needs "$DOMAIN" — but expansion also makes backticks live
# command substitution. The shell runs the command and substitutes its output
# into the file being written.
#
# The linter catches only the cases where the substituted command is itself
# suspect. It found `return 404` in setup-mcp-subdomain.sh because return
# outside a function is an error; it says nothing about `date` or `hostname`,
# which would silently vary the generated config from run to run. The shipped
# bug was a comment reading "already closes with `return 404`" that reached
# every generated config with those words deleted.
#
# An escaped backtick (\`) is literal even in an unquoted heredoc and is the
# correct way to write one — deploy-vayupress.sh does exactly that, and this
# audit must not punish it. Verified both ways before this gate was written.
#
# Usage: scripts/heredoc-audit.sh [files...]   (defaults to scripts/ and deploy/)

set -uo pipefail

files=("$@")
if [ ${#files[@]} -eq 0 ]; then
	mapfile -t files < <(find scripts deploy -name '*.sh' -type f 2>/dev/null | sort)
fi

found=0
for f in "${files[@]}"; do
	[ -f "$f" ] || continue
	out=$(awk -v F="$f" '
		{
			if (inh) {
				if ($0 ~ "^[[:space:]]*"delim"[[:space:]]*$") { inh = 0; next }
				# Drop escaped backticks first; whatever remains is live.
				probe = $0
				gsub(/\\`/, "", probe)
				if (index(probe, "`") > 0) {
					printf "%s:%d: unescaped backtick inside unquoted heredoc <<%s\n", F, NR, delim
					printf "    %s\n", $0
				}
				next
			}
			if (match($0, /<<-?[A-Za-z_][A-Za-z0-9_]*[[:space:]]*$/)) {
				d = substr($0, RSTART, RLENGTH)
				sub(/^<<-?/, "", d); gsub(/[[:space:]]/, "", d)
				delim = d; inh = 1
			}
		}
	' "$f")
	if [ -n "$out" ]; then
		printf '%s\n' "$out"
		found=1
	fi
done

if [ "$found" -ne 0 ]; then
	cat >&2 <<'WHY'

Each line above runs as a command when the file is written, and its output
replaces the text. Escape it (\`), or quote the heredoc delimiter if the body
needs no expansion at all.
WHY
	exit 1
fi

echo "✅ no unescaped backticks inside an unquoted heredoc"
