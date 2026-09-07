#!/bin/sh
set -e

BINARY="mping"
UID_NOW="$(id -u)"

if [ -z "${INSTALL_DIR:-}" ]; then
  if [ "$UID_NOW" -eq 0 ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="${HOME}/.local/bin"
  fi
fi

if [ ! -f "./$BINARY" ]; then
  echo "Error: $BINARY not found in current directory"
  exit 1
fi

echo "Installing $BINARY to $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR"
cp "./$BINARY" "$INSTALL_DIR/$BINARY"
if [ "$UID_NOW" -eq 0 ]; then
  chown root "$INSTALL_DIR/$BINARY"
fi
chmod 755 "$INSTALL_DIR/$BINARY"

OS="$(uname -s)"
case "$OS" in
  Linux)
    if [ "$UID_NOW" -eq 0 ] && command -v setcap > /dev/null 2>&1; then
      echo "Setting CAP_NET_RAW capability..."
      setcap cap_net_raw+ep "$INSTALL_DIR/$BINARY"
    else
      echo "Installed for non-privileged ping. Raw-socket features require CAP_NET_RAW or sudo."
    fi
    ;;
  Darwin)
    echo "Code signing..."
    codesign --sign - --force "$INSTALL_DIR/$BINARY"
    echo "Installed for non-privileged ping. Raw-socket features require sudo."
    ;;
  *)
    echo "Warning: Unsupported OS '$OS'. Run with the privileges needed for raw ICMP sockets."
    ;;
esac

echo "Done! You can now run: $BINARY"
