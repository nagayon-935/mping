#!/bin/sh
set -e

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="mping"

if [ "$(id -u)" -ne 0 ]; then
  echo "Please run as root (e.g., sudo ./install.sh)"
  exit 1
fi

if [ ! -f "./$BINARY" ]; then
  echo "Error: $BINARY not found in current directory"
  exit 1
fi

echo "Installing $BINARY to $INSTALL_DIR..."
cp "./$BINARY" "$INSTALL_DIR/$BINARY"
chown root "$INSTALL_DIR/$BINARY"
chmod 755 "$INSTALL_DIR/$BINARY"

OS="$(uname -s)"
case "$OS" in
  Linux)
    if command -v setcap > /dev/null 2>&1; then
      echo "Setting CAP_NET_RAW capability..."
      setcap cap_net_raw+ep "$INSTALL_DIR/$BINARY"
    else
      echo "setcap not found. Run with sudo, or install setcap and reinstall."
    fi
    ;;
  Darwin)
    echo "Code signing..."
    codesign --sign - --force "$INSTALL_DIR/$BINARY"
    echo "Run mping with sudo on macOS."
    ;;
  *)
    echo "Warning: Unsupported OS '$OS'. Run with the privileges needed for raw ICMP sockets."
    ;;
esac

echo "Done! You can now run: $BINARY"
