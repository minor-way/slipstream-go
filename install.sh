#!/bin/bash
#
# Slipstream-Go Quick Installer
# Downloads and installs pre-built binaries for your platform
#

set -e

VERSION="v1.1.0"
REPO="minor-way/slipstream-go"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}"
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║           Slipstream-Go Quick Installer                   ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo -e "${NC}"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
    linux*)  OS="linux" ;;
    darwin*) OS="darwin" ;;
    mingw*|msys*|cygwin*) 
        echo -e "${RED}Windows detected. Please download manually from:${NC}"
        echo "https://github.com/${REPO}/releases/latest"
        exit 1
        ;;
    *)
        echo -e "${RED}Unsupported OS: $OS${NC}"
        exit 1
        ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        echo -e "${RED}Unsupported architecture: $ARCH${NC}"
        exit 1
        ;;
esac

echo -e "${GREEN}[✓]${NC} Detected: ${OS}/${ARCH}"

# Build download URL
ARCHIVE="slipstream-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

# Create temp directory
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

# Download
echo -e "${BLUE}[i]${NC} Downloading ${ARCHIVE}..."
if command -v curl &> /dev/null; then
    curl -sL "$URL" -o "${TEMP_DIR}/${ARCHIVE}"
elif command -v wget &> /dev/null; then
    wget -q "$URL" -O "${TEMP_DIR}/${ARCHIVE}"
else
    echo -e "${RED}Error: curl or wget required${NC}"
    exit 1
fi

# Extract
echo -e "${BLUE}[i]${NC} Extracting..."
tar -xzf "${TEMP_DIR}/${ARCHIVE}" -C "${TEMP_DIR}"

# Install
echo -e "${BLUE}[i]${NC} Installing to ${INSTALL_DIR}..."

if [ -w "$INSTALL_DIR" ]; then
    mv "${TEMP_DIR}/slipstream-server-${OS}-${ARCH}" "${INSTALL_DIR}/slipstream-server"
    mv "${TEMP_DIR}/slipstream-client-${OS}-${ARCH}" "${INSTALL_DIR}/slipstream-client"
    chmod +x "${INSTALL_DIR}/slipstream-server" "${INSTALL_DIR}/slipstream-client"
else
    echo -e "${YELLOW}[!]${NC} Need sudo to install to ${INSTALL_DIR}"
    sudo mv "${TEMP_DIR}/slipstream-server-${OS}-${ARCH}" "${INSTALL_DIR}/slipstream-server"
    sudo mv "${TEMP_DIR}/slipstream-client-${OS}-${ARCH}" "${INSTALL_DIR}/slipstream-client"
    sudo chmod +x "${INSTALL_DIR}/slipstream-server" "${INSTALL_DIR}/slipstream-client"
fi

echo ""
echo -e "${GREEN}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║              Installation Complete!                       ║${NC}"
echo -e "${GREEN}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""
echo "Binaries installed to:"
echo "  - ${INSTALL_DIR}/slipstream-server"
echo "  - ${INSTALL_DIR}/slipstream-client"
echo ""
echo "Quick start:"
echo "  1. Generate keys:  slipstream-server --gen-key --privkey-file server.key --pubkey-file server.pub"
echo "  2. Run server:     slipstream-server --domain your.domain.com --privkey-file server.key"
echo "  3. Run client:     slipstream-client --domain your.domain.com --resolver SERVER_IP:5353 --pubkey-file server.pub"
echo ""
echo "For more info: https://github.com/${REPO}"
