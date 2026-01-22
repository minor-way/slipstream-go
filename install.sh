#!/bin/bash
#
# Slipstream-Go Quick Installer & Deployer
# Downloads binaries, configures, and creates systemd service
#

set -e

VERSION="v1.1.0"
REPO="minor-way/slipstream-go"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/slipstream"
SERVICE_USER="slipstream"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

print_banner() {
    echo -e "${CYAN}"
    echo "╔═══════════════════════════════════════════════════════════╗"
    echo "║           Slipstream-Go Quick Installer                   ║"
    echo "║        DNS Tunneling with QUIC Protocol                   ║"
    echo "╚═══════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

print_success() { echo -e "${GREEN}[✓]${NC} $1"; }
print_error() { echo -e "${RED}[✗]${NC} $1"; }
print_info() { echo -e "${BLUE}[i]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[!]${NC} $1"; }

check_root() {
    if [ "$EUID" -ne 0 ]; then
        print_error "This script must be run as root (use sudo)"
        exit 1
    fi
}

detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$OS" in
        linux*)  OS="linux" ;;
        darwin*) OS="darwin" ;;
        *)
            print_error "Unsupported OS: $OS (only Linux is supported for service installation)"
            exit 1
            ;;
    esac

    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)
            print_error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    print_success "Detected platform: ${OS}/${ARCH}"
}

download_binaries() {
    print_info "Downloading Slipstream-Go ${VERSION}..."

    ARCHIVE="slipstream-${VERSION}-${OS}-${ARCH}.tar.gz"
    URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

    TEMP_DIR=$(mktemp -d)
    trap "rm -rf $TEMP_DIR" EXIT

    echo -n "  Downloading ${ARCHIVE}... "
    if command -v curl &> /dev/null; then
        curl -sL "$URL" -o "${TEMP_DIR}/${ARCHIVE}"
    elif command -v wget &> /dev/null; then
        wget -q "$URL" -O "${TEMP_DIR}/${ARCHIVE}"
    else
        print_error "curl or wget required"
        exit 1
    fi
    echo -e "${GREEN}done${NC}"

    echo -n "  Extracting... "
    tar -xzf "${TEMP_DIR}/${ARCHIVE}" -C "${TEMP_DIR}"
    echo -e "${GREEN}done${NC}"

    echo -n "  Installing to ${INSTALL_DIR}... "
    mv "${TEMP_DIR}/slipstream-server-${OS}-${ARCH}" "${INSTALL_DIR}/slipstream-server"
    mv "${TEMP_DIR}/slipstream-client-${OS}-${ARCH}" "${INSTALL_DIR}/slipstream-client"
    chmod +x "${INSTALL_DIR}/slipstream-server" "${INSTALL_DIR}/slipstream-client"
    echo -e "${GREEN}done${NC}"

    print_success "Binaries installed to ${INSTALL_DIR}/"
}

create_user() {
    if ! id "$SERVICE_USER" &>/dev/null; then
        print_info "Creating service user: ${SERVICE_USER}"
        useradd -r -s /bin/false "$SERVICE_USER" 2>/dev/null || true
    fi
}

setup_config_dir() {
    mkdir -p "$CONFIG_DIR"
    chown "$SERVICE_USER:$SERVICE_USER" "$CONFIG_DIR"
    chmod 750 "$CONFIG_DIR"
}

generate_keys() {
    print_info "Generating Ed25519 key pair..."

    "${INSTALL_DIR}/slipstream-server" --gen-key \
        --privkey-file "${CONFIG_DIR}/server.key" \
        --pubkey-file "${CONFIG_DIR}/server.pub"

    chown "$SERVICE_USER:$SERVICE_USER" "${CONFIG_DIR}/server.key" "${CONFIG_DIR}/server.pub"
    chmod 600 "${CONFIG_DIR}/server.key"
    chmod 644 "${CONFIG_DIR}/server.pub"

    print_success "Keys generated in ${CONFIG_DIR}/"
}

deploy_server() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}                    Server Configuration                    ${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo ""

    # Domain
    echo -e "${BLUE}Enter domain NS record (e.g., n.example.com):${NC}"
    read -r DOMAIN < /dev/tty
    if [[ -z "$DOMAIN" ]]; then
        print_error "Domain NS record is required"
        exit 1
    fi

    # DNS Port
    echo -e "${BLUE}Enter DNS server port [default: 53]:${NC}"
    read -r DNS_PORT < /dev/tty
    DNS_PORT=${DNS_PORT:-53}

    # Target Type
    echo -e "${BLUE}Select target type:${NC}"
    echo "  1) direct - Connect directly to targets (default)"
    echo "  2) socks5 - Route through upstream SOCKS5 proxy"
    read -r TARGET_CHOICE < /dev/tty

    TARGET_TYPE="direct"
    TARGET_ADDR=""
    case $TARGET_CHOICE in
        2|socks5)
            TARGET_TYPE="socks5"
            echo -e "${BLUE}Enter upstream SOCKS5 address (e.g., 127.0.0.1:7020):${NC}"
            read -r TARGET_ADDR < /dev/tty
            if [[ -z "$TARGET_ADDR" ]]; then
                print_error "SOCKS5 address is required"
                exit 1
            fi
            ;;
    esac

    # Max fragments
    echo -e "${BLUE}Enter max fragments per DNS response [default: 5]:${NC}"
    read -r MAX_FRAGS < /dev/tty
    MAX_FRAGS=${MAX_FRAGS:-5}

    # Generate keys
    generate_keys

    # Create systemd service
    print_info "Creating systemd service..."

    SERVICE_FILE="/etc/systemd/system/slipstream-server.service"

    # Build ExecStart command
    EXEC_CMD="${INSTALL_DIR}/slipstream-server --domain ${DOMAIN} --dns-port ${DNS_PORT} --target-type ${TARGET_TYPE} --privkey-file ${CONFIG_DIR}/server.key --max-frags ${MAX_FRAGS} --log-level info"

    if [[ -n "$TARGET_ADDR" ]]; then
        EXEC_CMD="${EXEC_CMD} --target ${TARGET_ADDR}"
    fi

    cat > "$SERVICE_FILE" << EOF
[Unit]
Description=Slipstream-Go DNS Tunnel Server
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=${EXEC_CMD}
Restart=always
RestartSec=5
LimitNOFILE=65535

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${CONFIG_DIR}
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

    # Reload and enable service
    systemctl daemon-reload
    systemctl enable slipstream-server
    systemctl start slipstream-server

    # Print summary
    echo ""
    echo -e "${GREEN}╔═══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║              Server Installation Complete!                ║${NC}"
    echo -e "${GREEN}╚═══════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "${CYAN}Configuration:${NC}"
    echo "  Domain:       ${DOMAIN}"
    echo "  DNS Port:     ${DNS_PORT}"
    echo "  Target Type:  ${TARGET_TYPE}"
    [[ -n "$TARGET_ADDR" ]] && echo "  Target:       ${TARGET_ADDR}"
    echo "  Max Frags:    ${MAX_FRAGS}"
    echo ""
    echo -e "${CYAN}Files:${NC}"
    echo "  Private Key:  ${CONFIG_DIR}/server.key"
    echo "  Public Key:   ${CONFIG_DIR}/server.pub"
    echo "  Service:      ${SERVICE_FILE}"
    echo ""
    echo -e "${CYAN}Public Key (copy this for clients):${NC}"
    echo -e "${YELLOW}"
    cat "${CONFIG_DIR}/server.pub"
    echo -e "${NC}"
    echo -e "${CYAN}Service Commands:${NC}"
    echo "  Status:   sudo systemctl status slipstream-server"
    echo "  Logs:     sudo journalctl -u slipstream-server -f"
    echo "  Stop:     sudo systemctl stop slipstream-server"
    echo "  Start:    sudo systemctl start slipstream-server"
    echo "  Restart:  sudo systemctl restart slipstream-server"
    echo "  Disable:  sudo systemctl disable slipstream-server"
    echo ""
}

deploy_client() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}                    Client Configuration                    ${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo ""

    # Domain
    echo -e "${BLUE}Enter domain NS record (e.g., n.example.com):${NC}"
    read -r DOMAIN < /dev/tty
    if [[ -z "$DOMAIN" ]]; then
        print_error "Domain NS record is required"
        exit 1
    fi

    # Resolver
    echo -e "${BLUE}Enter DNS resolver address (e.g., 8.8.8.8:53):${NC}"
    read -r RESOLVER < /dev/tty
    if [[ -z "$RESOLVER" ]]; then
        print_error "Resolver is required"
        exit 1
    fi

    # Listen address
    echo -e "${BLUE}Enter local SOCKS5 listen address [default: 127.0.0.1:1080]:${NC}"
    read -r LISTEN_ADDR < /dev/tty
    LISTEN_ADDR=${LISTEN_ADDR:-127.0.0.1:1080}

    # Public key
    echo -e "${BLUE}Enter server public key (base64 string from server):${NC}"
    read -r PUBKEY < /dev/tty
    if [[ -z "$PUBKEY" ]]; then
        print_error "Public key is required"
        exit 1
    fi

    # Save public key
    setup_config_dir
    echo "$PUBKEY" > "${CONFIG_DIR}/server.pub"
    chmod 644 "${CONFIG_DIR}/server.pub"

    # Create systemd service
    print_info "Creating systemd service..."

    SERVICE_FILE="/etc/systemd/system/slipstream-client.service"

    cat > "$SERVICE_FILE" << EOF
[Unit]
Description=Slipstream-Go DNS Tunnel Client
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=${INSTALL_DIR}/slipstream-client --domain ${DOMAIN} --resolver ${RESOLVER} --listen ${LISTEN_ADDR} --pubkey-file ${CONFIG_DIR}/server.pub --log-level info
Restart=always
RestartSec=5
LimitNOFILE=65535

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${CONFIG_DIR}
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

    # Reload and enable service
    systemctl daemon-reload
    systemctl enable slipstream-client
    systemctl start slipstream-client

    # Print summary
    echo ""
    echo -e "${GREEN}╔═══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║              Client Installation Complete!                ║${NC}"
    echo -e "${GREEN}╚═══════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "${CYAN}Configuration:${NC}"
    echo "  Domain:       ${DOMAIN}"
    echo "  Resolver:     ${RESOLVER}"
    echo "  Listen:       ${LISTEN_ADDR}"
    echo "  Public Key:   ${CONFIG_DIR}/server.pub"
    echo ""
    echo -e "${CYAN}Service Commands:${NC}"
    echo "  Status:   sudo systemctl status slipstream-client"
    echo "  Logs:     sudo journalctl -u slipstream-client -f"
    echo "  Stop:     sudo systemctl stop slipstream-client"
    echo "  Start:    sudo systemctl start slipstream-client"
    echo "  Restart:  sudo systemctl restart slipstream-client"
    echo "  Disable:  sudo systemctl disable slipstream-client"
    echo ""
    echo -e "${CYAN}Test the tunnel:${NC}"
    echo "  curl -x socks5://${LISTEN_ADDR} https://ifconfig.me"
    echo ""
}

uninstall() {
    echo ""
    print_warning "This will remove Slipstream-Go completely"
    echo -e "${BLUE}Continue? [y/N]:${NC}"
    read -r CONFIRM < /dev/tty

    if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
        echo "Cancelled"
        exit 0
    fi

    # Stop and disable services
    systemctl stop slipstream-server 2>/dev/null || true
    systemctl stop slipstream-client 2>/dev/null || true
    systemctl disable slipstream-server 2>/dev/null || true
    systemctl disable slipstream-client 2>/dev/null || true

    # Remove files
    rm -f "${INSTALL_DIR}/slipstream-server"
    rm -f "${INSTALL_DIR}/slipstream-client"
    rm -f /etc/systemd/system/slipstream-server.service
    rm -f /etc/systemd/system/slipstream-client.service
    rm -rf "${CONFIG_DIR}"

    # Remove user
    userdel "$SERVICE_USER" 2>/dev/null || true

    systemctl daemon-reload

    print_success "Slipstream-Go has been uninstalled"
}

show_help() {
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  server      Install and configure as DNS tunnel server"
    echo "  client      Install and configure as DNS tunnel client"
    echo "  uninstall   Remove Slipstream-Go completely"
    echo "  help        Show this help message"
    echo ""
    echo "Examples:"
    echo "  sudo bash install.sh           # Interactive menu"
    echo "  sudo bash install.sh server    # Direct server install"
    echo "  sudo bash install.sh client    # Direct client install"
    echo ""
    echo "One-liner install:"
    echo "  bash <(curl -Ls https://raw.githubusercontent.com/${REPO}/main/install.sh)"
}

main_menu() {
    echo ""
    echo "Select installation type:"
    echo ""
    echo "  1) Server - DNS tunnel server (has internet access)"
    echo "  2) Client - DNS tunnel client (connects through server)"
    echo "  3) Uninstall"
    echo "  4) Exit"
    echo ""
    echo -n "Choice [1-4]: "
    read -r CHOICE < /dev/tty

    case $CHOICE in
        1) deploy_server ;;
        2) deploy_client ;;
        3) uninstall ;;
        4) exit 0 ;;
        *) print_error "Invalid choice"; exit 1 ;;
    esac
}

# Main
print_banner

case "${1:-}" in
    server)
        check_root
        detect_platform
        download_binaries
        create_user
        setup_config_dir
        deploy_server
        ;;
    client)
        check_root
        detect_platform
        download_binaries
        create_user
        setup_config_dir
        deploy_client
        ;;
    uninstall)
        check_root
        uninstall
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        check_root
        detect_platform
        download_binaries
        create_user
        setup_config_dir
        main_menu
        ;;
esac
