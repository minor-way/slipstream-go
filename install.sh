#!/bin/bash
#
# Slipstream-Go Quick Installer & Deployer
# Downloads binaries, configures, and creates systemd service
#

set -e

# Save script content for later installation (when piped, $0 is not a file)
SCRIPT_CONTENT_FILE="/tmp/slipstream-install-$$.sh"
if [ ! -f "$0" ] || [ "$0" = "bash" ] || [ "$0" = "/bin/bash" ]; then
    # Running from pipe - we're already executing, can't save stdin
    # Will download fresh copy later
    SCRIPT_CONTENT_FILE=""
else
    # Running from file - copy it
    cp "$0" "$SCRIPT_CONTENT_FILE" 2>/dev/null || SCRIPT_CONTENT_FILE=""
fi

VERSION="v1.1.0"
REPO="minor-way/slipstream-go"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/slipstream"
SERVICE_USER="slipstream"
SCRIPT_INSTALL_PATH="/usr/local/bin/slipstream-deploy"
SCRIPT_URL="https://raw.githubusercontent.com/${REPO}/main/install.sh"

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
print_question() { echo -ne "${BLUE}[?]${NC} $1"; }

check_root() {
    if [ "$EUID" -ne 0 ]; then
        print_error "This script must be run as root (use sudo)"
        exit 1
    fi
}

# Install the script itself to /usr/local/bin
install_script() {
    print_info "Installing slipstream-deploy script..."

    if [ -n "$SCRIPT_CONTENT_FILE" ] && [ -f "$SCRIPT_CONTENT_FILE" ]; then
        # Use saved script content
        cp "$SCRIPT_CONTENT_FILE" "$SCRIPT_INSTALL_PATH"
        rm -f "$SCRIPT_CONTENT_FILE"
    else
        # Download fresh copy (with cache bypass)
        curl -sL "${SCRIPT_URL}?t=$(date +%s)" -o "$SCRIPT_INSTALL_PATH"
    fi
    chmod +x "$SCRIPT_INSTALL_PATH"

    print_success "Script installed to $SCRIPT_INSTALL_PATH"
    print_info "You can now run 'slipstream-deploy' from anywhere"
}

detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$OS" in
        linux*)  OS="linux" ;;
        darwin*) OS="darwin" ;;
        *)
            print_error "Unsupported OS: $OS (only Linux supported for service installation)"
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
    # Check if already installed
    if [ -f "${INSTALL_DIR}/slipstream-server" ] && [ -f "${INSTALL_DIR}/slipstream-client" ]; then
        print_success "Binaries already installed in ${INSTALL_DIR}/"
        return 0
    fi

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
    chown "$SERVICE_USER:$SERVICE_USER" "$CONFIG_DIR" 2>/dev/null || true
    chmod 750 "$CONFIG_DIR"
}

generate_keys() {
    if [ -f "${CONFIG_DIR}/server.key" ] && [ -f "${CONFIG_DIR}/server.pub" ]; then
        print_success "Keys already exist in ${CONFIG_DIR}/"
        return 0
    fi

    print_info "Generating Ed25519 key pair..."

    "${INSTALL_DIR}/slipstream-server" --gen-key \
        --privkey-file "${CONFIG_DIR}/server.key" \
        --pubkey-file "${CONFIG_DIR}/server.pub"

    chown "$SERVICE_USER:$SERVICE_USER" "${CONFIG_DIR}/server.key" "${CONFIG_DIR}/server.pub" 2>/dev/null || true
    chmod 600 "${CONFIG_DIR}/server.key"
    chmod 644 "${CONFIG_DIR}/server.pub"

    print_success "Keys generated in ${CONFIG_DIR}/"
}

install_iptables_persistent() {
    # Check if already installed
    if command -v netfilter-persistent &> /dev/null; then
        return 0
    fi

    print_info "Installing iptables-persistent..."

    # Detect package manager and install
    if command -v apt-get &> /dev/null; then
        # Debian/Ubuntu
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -qq
        apt-get install -y -qq iptables-persistent netfilter-persistent
    elif command -v dnf &> /dev/null; then
        # Fedora/RHEL 8+
        dnf install -y -q iptables-services
        systemctl enable iptables 2>/dev/null || true
    elif command -v yum &> /dev/null; then
        # CentOS/RHEL 7
        yum install -y -q iptables-services
        systemctl enable iptables 2>/dev/null || true
    fi

    print_success "iptables-persistent installed"
}

configure_firewall() {
    local port="$1"

    # Check for ufw (Ubuntu/Debian)
    if command -v ufw &> /dev/null && ufw status | grep -q "active"; then
        print_info "Configuring ufw firewall..."
        ufw allow 53/udp >/dev/null 2>&1
        ufw allow "$port"/udp >/dev/null 2>&1
        print_success "ufw rules added for ports 53 and ${port}"
    fi

    # Check for firewalld (CentOS/RHEL/Fedora)
    if command -v firewall-cmd &> /dev/null && systemctl is-active --quiet firewalld; then
        print_info "Configuring firewalld..."
        firewall-cmd --permanent --add-port=53/udp >/dev/null 2>&1
        firewall-cmd --permanent --add-port="$port"/udp >/dev/null 2>&1
        firewall-cmd --reload >/dev/null 2>&1
        print_success "firewalld rules added for ports 53 and ${port}"
    fi
}

setup_iptables_redirect() {
    local target_port="$1"

    # Skip if target port is 53 (no redirect needed)
    if [ "$target_port" = "53" ]; then
        return 0
    fi

    # Install iptables-persistent if needed
    install_iptables_persistent

    # Configure firewall (ufw/firewalld)
    configure_firewall "$target_port"

    print_info "Setting up iptables redirect from port 53 to ${target_port}..."

    # Get primary network interface
    local interface
    interface=$(ip route | grep default | awk '{print $5}' | head -1)
    if [ -z "$interface" ]; then
        interface="eth0"
    fi

    # Remove existing redirect rules (if any)
    iptables -t nat -D PREROUTING -i "$interface" -p udp --dport 53 -j REDIRECT --to-ports "$target_port" 2>/dev/null || true

    # Add new redirect rule
    iptables -t nat -A PREROUTING -i "$interface" -p udp --dport 53 -j REDIRECT --to-ports "$target_port"

    # Allow traffic on target port
    iptables -I INPUT -p udp --dport "$target_port" -j ACCEPT 2>/dev/null || true

    # Save iptables rules persistently
    if command -v netfilter-persistent &> /dev/null; then
        netfilter-persistent save >/dev/null 2>&1 || true
    elif [ -d /etc/iptables ]; then
        mkdir -p /etc/iptables
        iptables-save > /etc/iptables/rules.v4 2>/dev/null || true
    elif [ -d /etc/sysconfig ]; then
        iptables-save > /etc/sysconfig/iptables 2>/dev/null || true
    fi

    print_success "iptables redirect configured: 53 -> ${target_port} (persistent)"
}

get_server_input() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}                    Server Configuration                    ${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo ""

    # Domain NS record
    while true; do
        print_question "Enter domain NS record (e.g., n.example.com): "
        read -r DOMAIN
        if [[ -n "$DOMAIN" ]]; then
            break
        else
            print_error "Domain NS record is required"
        fi
    done

    # DNS Port
    print_question "Enter DNS server port [default: 5353]: "
    read -r DNS_PORT
    DNS_PORT=${DNS_PORT:-5353}

    # Target Type
    echo ""
    echo "Select target type:"
    echo "  1) direct - Connect directly to targets (default)"
    echo "  2) socks5 - Route through upstream SOCKS5 proxy"
    print_question "Enter choice (1 or 2): "
    read -r TARGET_CHOICE

    TARGET_TYPE="direct"
    TARGET_ADDR=""
    case $TARGET_CHOICE in
        2|socks5)
            TARGET_TYPE="socks5"
            print_question "Enter upstream SOCKS5 address (e.g., 127.0.0.1:7020): "
            read -r TARGET_ADDR
            if [[ -z "$TARGET_ADDR" ]]; then
                print_error "SOCKS5 address is required"
                exit 1
            fi
            ;;
    esac

    # Max fragments
    print_question "Enter max fragments per DNS response [default: 5]: "
    read -r MAX_FRAGS
    MAX_FRAGS=${MAX_FRAGS:-5}

    print_info "Configuration:"
    print_info "  Domain NS record: $DOMAIN"
    print_info "  DNS Port: $DNS_PORT"
    print_info "  Target Type: $TARGET_TYPE"
    [[ -n "$TARGET_ADDR" ]] && print_info "  Target: $TARGET_ADDR"
    print_info "  Max Frags: $MAX_FRAGS"
}

deploy_server() {
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

    # Setup iptables redirect (53 -> target port)
    setup_iptables_redirect "$DNS_PORT"

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

get_client_input() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}                    Client Configuration                    ${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo ""

    # Domain NS record
    while true; do
        print_question "Enter domain NS record (e.g., n.example.com): "
        read -r DOMAIN
        if [[ -n "$DOMAIN" ]]; then
            break
        else
            print_error "Domain NS record is required"
        fi
    done

    # Resolver
    while true; do
        print_question "Enter DNS resolver address (e.g., 8.8.8.8:53): "
        read -r RESOLVER
        if [[ -n "$RESOLVER" ]]; then
            break
        else
            print_error "DNS resolver is required"
        fi
    done

    # Listen address
    print_question "Enter local SOCKS5 listen address [default: 127.0.0.1:1080]: "
    read -r LISTEN_ADDR
    LISTEN_ADDR=${LISTEN_ADDR:-127.0.0.1:1080}

    # Public key
    while true; do
        print_question "Enter server public key (base64 string from server): "
        read -r PUBKEY
        if [[ -n "$PUBKEY" ]]; then
            break
        else
            print_error "Server public key is required"
        fi
    done

    print_info "Configuration:"
    print_info "  Domain NS record: $DOMAIN"
    print_info "  Resolver: $RESOLVER"
    print_info "  Listen: $LISTEN_ADDR"
}

deploy_client() {
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

show_config_info() {
    print_info "Current Configuration"
    echo ""

    if [ -f "${CONFIG_DIR}/server.pub" ]; then
        echo -e "${CYAN}Public Key:${NC}"
        echo -e "${YELLOW}$(cat ${CONFIG_DIR}/server.pub)${NC}"
        echo ""
    fi

    if systemctl is-active --quiet slipstream-server; then
        echo -e "${CYAN}Server Status:${NC} ${GREEN}Running${NC}"
        systemctl status slipstream-server --no-pager -l 2>/dev/null | head -10
    elif systemctl is-active --quiet slipstream-client; then
        echo -e "${CYAN}Client Status:${NC} ${GREEN}Running${NC}"
        systemctl status slipstream-client --no-pager -l 2>/dev/null | head -10
    else
        print_warning "No slipstream service is running"
    fi
}

uninstall() {
    echo ""
    print_warning "This will remove Slipstream-Go completely"
    print_question "Continue? [y/N]: "
    read -r CONFIRM

    if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
        echo "Cancelled"
        return
    fi

    print_info "Removing Slipstream-Go..."

    # Stop and disable services
    systemctl stop slipstream-server 2>/dev/null || true
    systemctl stop slipstream-client 2>/dev/null || true
    systemctl disable slipstream-server 2>/dev/null || true
    systemctl disable slipstream-client 2>/dev/null || true

    # Remove files
    rm -f "${INSTALL_DIR}/slipstream-server"
    rm -f "${INSTALL_DIR}/slipstream-client"
    rm -f "$SCRIPT_INSTALL_PATH"
    rm -f /etc/systemd/system/slipstream-server.service
    rm -f /etc/systemd/system/slipstream-client.service
    rm -rf "${CONFIG_DIR}"

    # Remove user
    userdel "$SERVICE_USER" 2>/dev/null || true

    systemctl daemon-reload

    print_success "Slipstream-Go has been uninstalled"
}

show_menu() {
    echo ""
    print_info "Slipstream-Go Management"
    echo ""
    echo "  1) Install/Configure Server"
    echo "  2) Install/Configure Client"
    echo "  3) Check service status"
    echo "  4) View service logs"
    echo "  5) Show configuration info"
    echo "  6) Uninstall"
    echo "  0) Exit"
    echo ""
    print_question "Please select an option (0-6): "
}

handle_menu() {
    while true; do
        show_menu
        read -r choice

        case $choice in
            1)
                get_server_input
                deploy_server
                ;;
            2)
                get_client_input
                deploy_client
                ;;
            3)
                echo ""
                if [ -f /etc/systemd/system/slipstream-server.service ]; then
                    systemctl status slipstream-server --no-pager -l
                elif [ -f /etc/systemd/system/slipstream-client.service ]; then
                    systemctl status slipstream-client --no-pager -l
                else
                    print_warning "No slipstream service installed"
                fi
                ;;
            4)
                echo ""
                if [ -f /etc/systemd/system/slipstream-server.service ]; then
                    print_info "Showing slipstream-server logs (Ctrl+C to exit)..."
                    journalctl -u slipstream-server -f
                elif [ -f /etc/systemd/system/slipstream-client.service ]; then
                    print_info "Showing slipstream-client logs (Ctrl+C to exit)..."
                    journalctl -u slipstream-client -f
                else
                    print_warning "No slipstream service installed"
                fi
                ;;
            5)
                show_config_info
                ;;
            6)
                uninstall
                ;;
            0)
                print_info "Goodbye!"
                exit 0
                ;;
            *)
                print_error "Invalid choice. Please enter 0-6."
                ;;
        esac

        if [ "$choice" != "4" ]; then
            echo ""
            print_question "Press Enter to continue..."
            read -r
        fi
    done
}

# Main
print_banner
check_root

# If running from pipe (not installed location), install and show instructions
if [ "$0" != "$SCRIPT_INSTALL_PATH" ]; then
    print_info "First-time setup - installing script and binaries..."

    detect_platform
    download_binaries
    create_user
    setup_config_dir
    install_script

    print_success "Installation complete!"
    echo ""

    # Try to run interactively if TTY is available
    if [ -e /dev/tty ]; then
        print_info "Starting configuration menu..."
        echo ""
        exec "$SCRIPT_INSTALL_PATH" < /dev/tty
    else
        # No TTY (e.g., running via automated SSH)
        echo -e "${CYAN}To configure Slipstream, run:${NC}"
        echo ""
        echo -e "  ${YELLOW}sudo slipstream-deploy${NC}"
        echo ""
    fi
    exit 0
fi

# Running from installed location - show menu
detect_platform
download_binaries
create_user
setup_config_dir
handle_menu
