#!/bin/sh
set -e

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="mping"
allow_setuid=${MPING_ALLOW_SETUID:-0}
if [ "$#" -gt 0 ]; then
  if [ "$#" -ne 1 ] || [ "$1" != --allow-setuid ]; then
    echo "Usage: ./install.sh [--allow-setuid]" >&2
    exit 1
  fi
  allow_setuid=1
fi

if [ "$(id -u)" -ne 0 ]; then
  echo "Please run as root (e.g., sudo ./install.sh)"
  exit 1
fi

if [ ! -f "./$BINARY" ]; then
  echo "Error: $BINARY not found in current directory"
  exit 1
fi

OS="$(uname -s)"
use_setcap=0
if [ "$OS" = Linux ]; then
  if command -v setcap >/dev/null 2>&1; then
    use_setcap=1
  elif [ "$allow_setuid" != 1 ]; then
    echo "setcap is missing. Install libcap2-bin (Debian/Ubuntu) or libcap (Fedora/Arch), then rerun sudo ./install.sh." >&2
    echo "To explicitly allow setuid root instead, run sudo ./install.sh --allow-setuid." >&2
    exit 1
  fi
fi

echo "Installing $BINARY to $INSTALL_DIR..."
cp "./$BINARY" "$INSTALL_DIR/$BINARY"
chmod 755 "$INSTALL_DIR/$BINARY"

case "$OS" in
  Linux)
    if [ "$use_setcap" = 1 ]; then
      echo "Setting CAP_NET_RAW capability..."
      setcap cap_net_raw+ep "$INSTALL_DIR/$BINARY"
    else
      echo "Setting setuid bit as requested..."
      chown root "$INSTALL_DIR/$BINARY"
      chmod u+s "$INSTALL_DIR/$BINARY"
    fi
    ;;
  Darwin)
    echo "Setting setuid bit (macOS)..."
    chown root "$INSTALL_DIR/$BINARY"
    chmod u+s "$INSTALL_DIR/$BINARY"
    echo "Code signing (macOS requires valid signature for setuid binaries)..."
    codesign --sign - --force "$INSTALL_DIR/$BINARY"
    ;;
  *)
    echo "Warning: Unsupported OS '$OS'. You may need to manually grant CAP_NET_RAW or set setuid."
    ;;
esac

echo "Done! You can now run: $BINARY"
