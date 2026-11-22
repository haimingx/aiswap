#!/usr/bin/env bash
set -euo pipefail

REPO="${AISWAP_REPO:-haimingx/aiswap}"
VERSION="${AISWAP_VERSION:-latest}"

detect_platform() {
  local os arch
  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux)  os="linux" ;;
    *) echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
  esac
  case "$(uname -m)" in
    arm64|aarch64) arch="arm64" ;;
    x86_64|amd64)  arch="amd64" ;;
    *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  echo "${os} ${arch}"
}

resolve_install_dir() {
  local os="$1"
  if [ -n "${AISWAP_INSTALL_DIR:-}" ]; then
    echo "$AISWAP_INSTALL_DIR"
    return
  fi
  if [ "$os" = "darwin" ]; then
    for dir in "/opt/homebrew/bin" "/usr/local/bin"; do
      if [ -d "$dir" ] && [ -w "$dir" ]; then
        echo "$dir"
        return
      fi
    done
  fi
  echo "$HOME/.local/bin"
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "Neither sha256sum nor shasum is installed." >&2
    exit 1
  fi
}

main() {
  read -r OS ARCH <<<"$(detect_platform)"
  local install_dir
  install_dir="$(resolve_install_dir "$OS")"

  TMP_DIR="$(mktemp -d)"
  TMP_BIN=""
  trap 'rm -rf "$TMP_DIR" ${TMP_BIN:+"$TMP_BIN"}' EXIT

  local use_gh=0
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    use_gh=1
  fi

  if [ "$VERSION" = "latest" ]; then
    if [ "$use_gh" -eq 1 ]; then
      VERSION="$(gh release view -R "$REPO" --json tagName -q .tagName 2>/dev/null || true)"
    fi
    if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
      VERSION="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" | sed -n 's#.*/tag/##p')"
    fi
  fi

  if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
    echo "Failed to resolve release version for ${REPO}." >&2
    exit 1
  fi

  local asset="aiswap-${VERSION}-${OS}-${ARCH}.tar.gz"
  echo "==> Downloading ${asset} (${VERSION})..."

  local downloaded=0
  if [ "$use_gh" -eq 1 ]; then
    if gh release download "$VERSION" -R "$REPO" -p "$asset" -D "$TMP_DIR" 2>/dev/null && \
       gh release download "$VERSION" -R "$REPO" -p "checksums.txt" -D "$TMP_DIR" 2>/dev/null; then
      downloaded=1
    fi
  fi
  if [ "$downloaded" -eq 0 ]; then
    curl -fsSL "https://github.com/${REPO}/releases/download/${VERSION}/${asset}" -o "${TMP_DIR}/${asset}"
    curl -fsSL "https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt" -o "${TMP_DIR}/checksums.txt"
  fi

  local expected actual
  expected="$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1; exit}' "${TMP_DIR}/checksums.txt")"
  actual="$(sha256_file "${TMP_DIR}/${asset}")"
  if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
    echo "SHA256 verification failed for ${asset}" >&2
    exit 1
  fi
  echo "==> SHA256 verified."

  tar -xzf "${TMP_DIR}/${asset}" -C "$TMP_DIR"
  mkdir -p "$install_dir"

  local target="${install_dir}/aiswap"
  TMP_BIN="${install_dir}/.aiswap.new.$$"
  cp "${TMP_DIR}/aiswap" "$TMP_BIN"
  chmod 0755 "$TMP_BIN"
  if [ "$OS" = "darwin" ]; then
    xattr -d com.apple.quarantine "$TMP_BIN" 2>/dev/null || true
    codesign --force --sign - "$TMP_BIN" 2>/dev/null || true
  fi
  mv -f "$TMP_BIN" "$target"
  TMP_BIN=""

  echo "==> Installed aiswap ${VERSION} -> ${target}"
  case ":${PATH}:" in
    *":${install_dir}:"*) ;;
    *)
      local rc_file="$HOME/.bashrc"
      if [ -n "${ZSH_VERSION:-}" ] || [[ "${SHELL:-}" == */zsh ]]; then
        rc_file="$HOME/.zshrc"
      fi
      echo ""
      echo "Add ${install_dir} to your PATH:"
      echo "  echo 'export PATH=\"${install_dir}:\$PATH\"' >> ${rc_file} && source ${rc_file}"
      ;;
  esac
}

main "$@"
