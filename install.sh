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
chmod 755 "$INSTALL_DIR/$BINARY"

OS="$(uname -s)"
case "$OS" in
  Linux)
    if command -v setcap > /dev/null 2>&1; then
      echo "Setting CAP_NET_RAW capability..."
      setcap cap_net_raw+ep "$INSTALL_DIR/$BINARY"
    else
      echo "setcap not found, falling back to setuid..."
      chown root "$INSTALL_DIR/$BINARY"
      chmod u+s "$INSTALL_DIR/$BINARY"
    fi
    ;;
  Darwin)
    echo "Setting setuid bit (macOS)..."
    chown root "$INSTALL_DIR/$BINARY"
    chmod u+s "$INSTALL_DIR/$BINARY"
    ;;
  *)
    echo "Warning: Unsupported OS '$OS'. You may need to manually grant CAP_NET_RAW or set setuid."
    ;;
esac

echo "Done! You can now run: $BINARY"
