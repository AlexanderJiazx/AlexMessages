# Sourced by publish-update.sh and publish-app.sh. Defines one function.
#
#   upload_verified <local file> <remote temporary path> <remote command>
#
# Copies <local file> to <remote temporary path> on the Pi over one SSH
# connection (PI, default alexander@35.226.215.209; PI_PORT, default 2222).
# Remote paths are relative to the SSH user's home directory. The remote
# script creates the parent directory of the temporary path, writes the
# file, and computes its SHA-256 (sha256sum, or shasum -a 256 when
# sha256sum does not exist). When the digest differs from the SHA-256 that
# the Mac computed, it deletes the temporary file, prints an error line to
# stderr, and exits non-zero without running <remote command>. Only when
# the digests are equal does it run <remote command>. A cut SSH stream
# therefore never reaches <remote command>. The function returns the exit
# status of ssh.
upload_verified() {
  local file=$1 tmp=$2 after=$3
  local sum dir pi port
  pi="${PI:-alexander@35.226.215.209}"
  port="${PI_PORT:-2222}"
  sum=$(shasum -a 256 "$file" | cut -d' ' -f1)
  dir=$(dirname "$tmp")
  ssh -p "$port" "$pi" "set -e
mkdir -p '$dir'
cat > '$tmp'
if command -v sha256sum >/dev/null 2>&1; then got=\$(sha256sum '$tmp' | cut -d' ' -f1); else got=\$(shasum -a 256 '$tmp' | cut -d' ' -f1); fi
if [ \"\$got\" != '$sum' ]; then
  rm -f '$tmp'
  echo 'Upload refused: SHA-256 of $tmp on the Pi differs from the file on the Mac; the temporary file was deleted.' >&2
  exit 1
fi
$after" < "$file"
}
