#!/bin/sh
set -eu

dir="${HELM_PLUGIN_DIR:?HELM_PLUGIN_DIR is not set}"
version="$(sed -n 's/^version:[[:space:]]*//p' "$dir/plugin.yaml" | tr -d '"')"
bin="$dir/bin/helm-bumpcheck"

if [ -x "$bin" ] && [ "$("$bin" --version 2>/dev/null | awk '{print $NF}')" = "$version" ]; then
  exit 0
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux | darwin) ;;
  *) echo "helm-bumpcheck: unsupported OS $os" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "helm-bumpcheck: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

archive="helm-bumpcheck_${version}_${os}_${arch}.tar.gz"
url="${HELM_BUMPCHECK_RELEASE_URL:-https://github.com/lov3g00d/helm-bumpcheck/releases/download}/v${version}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  else
    wget -q "$1" -O "$2"
  fi
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
  echo "helm-bumpcheck: checksum mismatch for $archive" >&2
  exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp" helm-bumpcheck
mkdir -p "$dir/bin"
mv "$tmp/helm-bumpcheck" "$bin"
chmod +x "$bin"
