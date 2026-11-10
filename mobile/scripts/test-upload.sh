#!/usr/bin/env bash
# Regression test of upload_verified (lib-upload.sh). It contacts no host: a
# stand-in `ssh` runs the remote script in a scratch "remote home" directory.
# With FAKE_SSH_CUT=1 the stand-in passes only the first half of the input
# bytes to the remote script, as a cut connection would.
#
#   bash mobile/scripts/test-upload.sh
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/home"

cat > "$work/bin/ssh" <<'FAKE'
#!/usr/bin/env bash
script="${!#}"
cd "$FAKE_REMOTE_HOME" || exit 99
if [[ "${FAKE_SSH_CUT:-}" == 1 ]]; then
  all=$(mktemp)
  cat > "$all"
  size=$(wc -c < "$all")
  head -c $((size / 2)) "$all" | bash -c "$script"
  status=$?
  rm -f "$all"
  exit $status
fi
exec bash -c "$script"
FAKE
chmod +x "$work/bin/ssh"
export PATH="$work/bin:$PATH"
export FAKE_REMOTE_HOME="$work/home"
unset FAKE_SSH_CUT

# shellcheck source=lib-upload.sh
source "$here/lib-upload.sh"

failures=0
check() { # <name> <condition exit status>
  if [[ "$2" -eq 0 ]]; then echo "PASS $1"; else echo "FAIL $1"; failures=$((failures + 1)); fi
}

head -c 200000 /dev/urandom > "$work/data.bin"

# Case 1: intact upload.
upload_verified "$work/data.bin" "app/.data.bin" "mv app/.data.bin app/data.bin" 2>/dev/null
rc=$?
ok=0
[[ $rc -eq 0 ]] || ok=1
cmp -s "$work/data.bin" "$work/home/app/data.bin" || ok=1
[[ ! -e "$work/home/app/.data.bin" ]] || ok=1
check "intact upload is published and leaves no temporary file" $ok

# Case 2: truncated upload.
FAKE_SSH_CUT=1 upload_verified "$work/data.bin" "app/.cut.bin" "mv app/.cut.bin app/cut.bin" 2>/dev/null
rc=$?
ok=0
[[ $rc -ne 0 ]] || ok=1
[[ ! -e "$work/home/app/cut.bin" ]] || ok=1
[[ ! -e "$work/home/app/.cut.bin" ]] || ok=1
check "truncated upload fails, is not published, and the temporary file is deleted" $ok

# Case 3: the tar flow of publish-update.sh.
mkdir -p "$work/src/sub"
echo one > "$work/src/a.txt"
head -c 5000 /dev/urandom > "$work/src/sub/b.bin"
COPYFILE_DISABLE=1 tar -C "$work/src" -cf "$work/update.tar" .
stamp=1700000000
version=1.2.0
upload_verified "$work/update.tar" "data/updates/$version/.$stamp.tar" \
  "d=data/updates/$version; mkdir -p \$d/.$stamp; tar -C \$d/.$stamp -xf \$d/.$stamp.tar; rm \$d/.$stamp.tar; mv \$d/.$stamp \$d/$stamp" 2>/dev/null
rc=$?
ok=0
[[ $rc -eq 0 ]] || ok=1
diff -r "$work/src" "$work/home/data/updates/$version/$stamp" >/dev/null 2>&1 || ok=1
[[ ! -e "$work/home/data/updates/$version/.$stamp" ]] || ok=1
[[ ! -e "$work/home/data/updates/$version/.$stamp.tar" ]] || ok=1
check "tar flow extracts identical files under the final name" $ok

exit $((failures > 0))
