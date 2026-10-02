#!/bin/sh
set -eu

fail() {
  echo "helm-bumpcheck: $1; run 'helm plugin uninstall bumpcheck' before installing again" >&2
  exit 1
}

dir="${HELM_PLUGIN_DIR:?HELM_PLUGIN_DIR is not set}"
version="$(sed -n 's/^version:[[:space:]]*//p' "$dir/plugin.yaml" | tr -d '"')"
bin="$dir/bin/helm-bumpcheck"

if [ -x "$bin" ] && [ "$("$bin" --version 2>/dev/null | awk '{print $NF}')" = "$version" ]; then
  exit 0
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux | darwin) ;;
  *) fail "unsupported OS $os" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

archive="helm-bumpcheck_${version}_${os}_${arch}.tar.gz"
base="${HELM_BUMPCHECK_RELEASE_URL:-https://github.com/lov3g00d/helm-bumpcheck/releases/download}"
url="$base/v${version}"
case "$base" in
  https://*) https=1 ;;
  *)
    https=0
    echo "helm-bumpcheck: warning: HELM_BUMPCHECK_RELEASE_URL $base does not use https" >&2
    ;;
esac
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fetch() {
  if command -v curl >/dev/null 2>&1; then
    if [ "$https" = 1 ]; then
      curl --proto '=https' --proto-redir '=https' -fsL "$1" -o "$2"
    else
      curl -fsL "$1" -o "$2"
    fi
  else
    wget -q "$1" -O "$2"
  fi || fail "could not download $1"
}

fetch "$url/$archive" "$tmp/$archive"
fetch "$url/checksums.txt" "$tmp/checksums.txt"

expected="$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  fail "checksum mismatch for $url/$archive"
fi

tar -xzf "$tmp/$archive" -C "$tmp" helm-bumpcheck 2>/dev/null || fail "no helm-bumpcheck binary in $url/$archive"
mkdir -p "$dir/bin"
mv "$tmp/helm-bumpcheck" "$bin"
chmod +x "$bin"
