#!/usr/bin/env bash
set -euo pipefail
# Native functional lab, Debian 13 / amd64. No services start during installation.
[[ "$(uname -s)" = Linux && "$(uname -m)" = x86_64 ]] || { echo 'This installer requires Linux amd64.' >&2; exit 1; }
as_root=()
if [[ "$EUID" != 0 ]]; then as_root=(sudo); fi
missing=()
for package in golang-go prometheus prometheus-alertmanager postgresql-17 curl ca-certificates make gcc libc6-dev python3 bubblewrap util-linux; do
  dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q 'install ok installed' || missing+=("$package")
done
if ((${#missing[@]})); then
  "${as_root[@]}" apt-get update -qq
  "${as_root[@]}" env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${missing[@]}"
fi
go version
prometheus --version 2>&1 | head -1
prometheus-alertmanager --version 2>&1 | head -1
root="$(cd "$(dirname "$0")" && pwd)"
if [[ ! -x "$root/.tools/grafana/bin/grafana" ]]; then
  mkdir -p "$root/.tools"
  archive="$(mktemp)"
  trap 'rm -f "$archive"' EXIT
  curl -fLsS https://dl.grafana.com/oss/release/grafana-12.3.0.linux-amd64.tar.gz -o "$archive"
  echo "dd34d03f8dbf815f153b638b793e6ab1b9d205b7c0a15dddd851332f0c9ac01c  $archive" | sha256sum -c -
  mkdir -p "$root/.tools/grafana"
  tar -xzf "$archive" -C "$root/.tools/grafana" --strip-components=1
fi
