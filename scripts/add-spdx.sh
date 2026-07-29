#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# add-spdx.sh LICENSE DIR: prepend an SPDX-License-Identifier header to every
# *.go file under DIR that lacks one. Code generators (sqlc) do not emit SPDX
# headers, so this runs after generation to keep every source file licensed.
# Idempotent: files that already declare a license are left untouched.
set -e

license="$1"
dir="$2"

if [ -z "$license" ] || [ -z "$dir" ]; then
	echo "usage: add-spdx.sh <license-id> <dir>" >&2
	exit 2
fi

# Only the first line counts as the license header. Generated files may embed
# the string "SPDX" deep inside string literals (e.g. sqlc inlining licensed
# SQL), so a whole-file grep would wrongly treat them as already licensed.
find "$dir" -name '*.go' | while IFS= read -r file; do
	case "$(head -n 1 "$file")" in
	*SPDX-License-Identifier*) continue ;;
	esac
	tmp="$file.spdx.tmp"
	printf '// SPDX-License-Identifier: %s\n\n' "$license" >"$tmp"
	cat "$file" >>"$tmp"
	mv "$tmp" "$file"
	echo "spdx: added $license to $file"
done
