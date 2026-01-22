#!/bin/bash
#
# Slipstream-Go Deployment Script
# DNS Tunneling with QUIC - Server & Client Setup
#

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="${SCRIPT_DIR}/bin"

# GitHub release info
GITHUB_REPO="minor-way/slipstream-go"
VERSION="v1.0.0"

print_banner() {
    echo -e "${CYAN}"
    echo "╔═══════════════════════════════════════════════════════════╗"
    echo "║              Slipstream-Go Deployment Tool                ║"
    echo "║          DNS Tunneling with QUIC Protocol                 ║"
    echo "╚═══════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
}

print_success() {
    echo -e "${GREEN}[✓]${NC} $1"
}

print_error() {
    echo -e "${RED}[✗]${NC} $1"
}

print_info() {
    echo -e "${BLUE}[i]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[!]${NC} $1"
}

detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$OS" in
        linux*)  OS="linux" ;;
        darwin*) OS="darwin" ;;
        mingw*|msys*|cygwin*) OS="windows" ;;
        *)
            print_error "Unsupported OS: $OS"
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

    print_info "Detected platform: ${OS}/${ARCH}"
}

download_binaries() {
    print_info "Downloading pre-built binaries..."

    mkdir -p "$BIN_DIR"

    # Determine file extension
    if [ "$OS" = "windows" ]; then
        EXT="zip"
        ARCHIVE_NAME="slipstream-${VERSION}-${OS}-${ARCH}.${EXT}"
    else
        EXT="tar.gz"
        ARCHIVE_NAME="slipstream-${VERSION}-${OS}-${ARCH}.${EXT}"
    fi

    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${ARCHIVE_NAME}"
    TEMP_DIR=$(mktemp -d)

    echo -n "  Downloading ${ARCHIVE_NAME}... "

    # Download
    if command -v curl &> /dev/null; then
        curl -sL "$DOWNLOAD_URL" -o "${TEMP_DIR}/${ARCHIVE_NAME}"
    elif command -v wget &> /dev/null; then
        wget -q "$DOWNLOAD_URL" -O "${TEMP_DIR}/${ARCHIVE_NAME}"
    else
        print_error "Neither curl nor wget found. Please install one of them."
        exit 1
    fi

    if [ ! -f "${TEMP_DIR}/${ARCHIVE_NAME}" ]; then
        print_error "Download failed"
        exit 1
    fi

    echo -e "${GREEN}done${NC}"

    # Extract
    echo -n "  Extracting... "

    if [ "$OS" = "windows" ]; then
        unzip -q "${TEMP_DIR}/${ARCHIVE_NAME}" -d "${TEMP_DIR}"
    else
        tar -xzf "${TEMP_DIR}/${ARCHIVE_NAME}" -C "${TEMP_DIR}"
    fi

    # Move binaries
    if [ "$OS" = "windows" ]; then
        mv "${TEMP_DIR}/slipstream-server-${OS}-${ARCH}.exe" "${BIN_DIR}/slipstream-server.exe"
        mv "${TEMP_DIR}/slipstream-client-${OS}-${ARCH}.exe" "${BIN_DIR}/slipstream-client.exe"
    else
        mv "${TEMP_DIR}/slipstream-server-${OS}-${ARCH}" "${BIN_DIR}/slipstream-server"
        mv "${TEMP_DIR}/slipstream-client-${OS}-${ARCH}" "${BIN_DIR}/slipstream-client"
        chmod +x "${BIN_DIR}/slipstream-server" "${BIN_DIR}/slipstream-client"
    fi

    # Cleanup
    rm -rf "$TEMP_DIR"

    echo -e "${GREEN}done${NC}"
    print_success "Binaries downloaded to ${BIN_DIR}/"
}

build_binaries() {
    print_info "Building binaries from source..."

    if ! command -v go &> /dev/null; then
        print_error "Go is not installed. Cannot build from source."
        exit 1
    fi

    mkdir -p "$BIN_DIR"

    echo -n "  Building server... "
    go build -o "${BIN_DIR}/slipstream-server" ./cmd/server
    echo -e "${GREEN}done${NC}"

    echo -n "  Building client... "
    go build -o "${BIN_DIR}/slipstream-client" ./cmd/client
    echo -e "${GREEN}done${NC}"

    print_success "Binaries built successfully"
}

get_binaries() {
    # Check if binaries already exist
    if [ -f "${BIN_DIR}/slipstream-server" ] && [ -f "${BIN_DIR}/slipstream-client" ]; then
        print_success "Binaries already exist in ${BIN_DIR}/"
        return 0
    fi

    detect_platform

    echo ""
    echo -e "${BLUE}How would you like to get the binaries?${NC}"
    echo "  1) Download pre-built binaries (recommended)"
    echo "  2) Build from source (requires Go)"
    read -r build_choice

    case $build_choice in
        2)
            build_binaries
            ;;
        *)
            download_binaries
            ;;
    esac
}

generate_keys() {
    local key_dir="$1"
    local name="$2"

    mkdir -p "$key_dir"

    local privkey="${key_dir}/${name}.key"
    local pubkey="${key_dir}/${name}.pub"

    local server_bin="${BIN_DIR}/slipstream-server"
    if [ "$OS" = "windows" ]; then
        server_bin="${BIN_DIR}/slipstream-server.exe"
    fi

    print_info "Generating Ed25519 key pair..."
    "$server_bin" --gen-key --privkey-file "$privkey" --pubkey-file "$pubkey" 2>/dev/null

    print_success "Keys generated:"
    echo "  Private key: $privkey"
    echo "  Public key:  $pubkey"

    # Display public key content for easy copying
    echo ""
    print_info "Public key (copy this for client configuration):"
    echo -e "${YELLOW}$(cat "$pubkey")${NC}"
}

deploy_server() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}                    Server Deployment                       ${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo ""

    local server_bin="${BIN_DIR}/slipstream-server"
    if [ "$OS" = "windows" ]; then
        server_bin="${BIN_DIR}/slipstream-server.exe"
    fi

    # Domains
    echo -e "${BLUE}Enter allowed domains (comma-separated, e.g., tunnel.example.com,t2.example.com):${NC}"
    read -r domains_input

    if [[ -z "$domains_input" ]]; then
        print_error "At least one domain is required"
        exit 1
    fi

    # Convert comma-separated to array
    IFS=',' read -ra DOMAINS <<< "$domains_input"

    # DNS Port
    echo -e "${BLUE}Enter DNS server port [default: 5353]:${NC}"
    read -r dns_port
    dns_port=${dns_port:-5353}

    # Target Type
    echo -e "${BLUE}Select target type:${NC}"
    echo "  1) direct - Connect directly to target hosts"
    echo "  2) socks5 - Route through upstream SOCKS5 proxy"
    read -r target_choice

    case $target_choice in
        1|direct)
            target_type="direct"
            target=""
            ;;
        2|socks5)
            target_type="socks5"
            echo -e "${BLUE}Enter upstream SOCKS5 address (e.g., 127.0.0.1:7020):${NC}"
            read -r target
            if [[ -z "$target" ]]; then
                print_error "SOCKS5 target address is required"
                exit 1
            fi
            ;;
        *)
            target_type="direct"
            target=""
            ;;
    esac

    # Key management
    echo -e "${BLUE}Key configuration:${NC}"
    echo "  1) Generate new key pair"
    echo "  2) Use existing private key file"
    read -r key_choice

    case $key_choice in
        1)
            echo -e "${BLUE}Enter directory for keys [default: ./keys]:${NC}"
            read -r key_dir
            key_dir=${key_dir:-./keys}
            generate_keys "$key_dir" "server"
            privkey_file="${key_dir}/server.key"
            ;;
        2)
            echo -e "${BLUE}Enter path to private key file:${NC}"
            read -r privkey_file
            if [[ ! -f "$privkey_file" ]]; then
                print_error "Private key file not found: $privkey_file"
                exit 1
            fi
            ;;
        *)
            print_error "Invalid choice"
            exit 1
            ;;
    esac

    # Log level
    echo -e "${BLUE}Select log level [default: info]:${NC}"
    echo "  1) debug"
    echo "  2) info"
    echo "  3) warn"
    echo "  4) error"
    read -r log_choice

    case $log_choice in
        1|debug) log_level="debug" ;;
        3|warn) log_level="warn" ;;
        4|error) log_level="error" ;;
        *) log_level="info" ;;
    esac

    # Memory limit
    echo -e "${BLUE}Enter memory limit in MB [default: 400]:${NC}"
    read -r memory_limit
    memory_limit=${memory_limit:-400}

    # Build command
    echo ""
    print_info "Configuration Summary:"
    echo "  Domains:      ${DOMAINS[*]}"
    echo "  DNS Port:     $dns_port"
    echo "  Target Type:  $target_type"
    [[ -n "$target" ]] && echo "  Target:       $target"
    echo "  Private Key:  $privkey_file"
    echo "  Log Level:    $log_level"
    echo "  Memory Limit: ${memory_limit}MB"
    echo ""

    # Build the command
    cmd="$server_bin"
    for domain in "${DOMAINS[@]}"; do
        domain=$(echo "$domain" | xargs) # trim whitespace
        cmd+=" --domain $domain"
    done
    cmd+=" --dns-port $dns_port"
    cmd+=" --target-type $target_type"
    [[ -n "$target" ]] && cmd+=" --target $target"
    cmd+=" --privkey-file $privkey_file"
    cmd+=" --log-level $log_level"
    cmd+=" --memory-limit $memory_limit"

    echo -e "${BLUE}Command to run:${NC}"
    echo "$cmd"
    echo ""

    echo -e "${BLUE}Start server now? [Y/n]:${NC}"
    read -r start_now

    if [[ "$start_now" != "n" && "$start_now" != "N" ]]; then
        print_info "Starting server..."
        exec $cmd
    else
        # Save to script
        local run_script="./run-server.sh"
        echo "#!/bin/bash" > "$run_script"
        echo "$cmd" >> "$run_script"
        chmod +x "$run_script"
        print_success "Saved run command to: $run_script"
    fi
}

deploy_client() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}                    Client Deployment                       ${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════${NC}"
    echo ""

    local client_bin="${BIN_DIR}/slipstream-client"
    if [ "$OS" = "windows" ]; then
        client_bin="${BIN_DIR}/slipstream-client.exe"
    fi

    # Domain
    echo -e "${BLUE}Enter tunnel domain (e.g., tunnel.example.com):${NC}"
    read -r domain

    if [[ -z "$domain" ]]; then
        print_error "Domain is required"
        exit 1
    fi

    # Resolver
    echo -e "${BLUE}Enter DNS resolver address [default: 127.0.0.1:5353]:${NC}"
    read -r resolver
    resolver=${resolver:-127.0.0.1:5353}

    # Listen address
    echo -e "${BLUE}Enter local SOCKS5 listen address [default: 127.0.0.1:1080]:${NC}"
    read -r listen_addr
    listen_addr=${listen_addr:-127.0.0.1:1080}

    # Public key
    echo -e "${BLUE}Public key configuration:${NC}"
    echo "  1) Enter public key as raw string (base64)"
    echo "  2) Use public key file"
    read -r pubkey_choice

    case $pubkey_choice in
        1)
            echo -e "${BLUE}Enter server public key (base64):${NC}"
            read -r pubkey_raw
            if [[ -z "$pubkey_raw" ]]; then
                print_error "Public key is required"
                exit 1
            fi
            # Save to temp file
            pubkey_file="./server.pub"
            echo "$pubkey_raw" > "$pubkey_file"
            print_info "Public key saved to: $pubkey_file"
            ;;
        2)
            echo -e "${BLUE}Enter path to public key file:${NC}"
            read -r pubkey_file
            if [[ ! -f "$pubkey_file" ]]; then
                print_error "Public key file not found: $pubkey_file"
                exit 1
            fi
            ;;
        *)
            print_error "Invalid choice"
            exit 1
            ;;
    esac

    # Log level
    echo -e "${BLUE}Select log level [default: info]:${NC}"
    echo "  1) debug"
    echo "  2) info"
    echo "  3) warn"
    echo "  4) error"
    read -r log_choice

    case $log_choice in
        1|debug) log_level="debug" ;;
        3|warn) log_level="warn" ;;
        4|error) log_level="error" ;;
        *) log_level="info" ;;
    esac

    # Summary
    echo ""
    print_info "Configuration Summary:"
    echo "  Domain:       $domain"
    echo "  Resolver:     $resolver"
    echo "  Listen:       $listen_addr"
    echo "  Public Key:   $pubkey_file"
    echo "  Log Level:    $log_level"
    echo ""

    # Build the command
    cmd="$client_bin"
    cmd+=" --domain $domain"
    cmd+=" --resolver $resolver"
    cmd+=" --listen $listen_addr"
    cmd+=" --pubkey-file $pubkey_file"
    cmd+=" --log-level $log_level"

    echo -e "${BLUE}Command to run:${NC}"
    echo "$cmd"
    echo ""

    echo -e "${BLUE}Start client now? [Y/n]:${NC}"
    read -r start_now

    if [[ "$start_now" != "n" && "$start_now" != "N" ]]; then
        print_info "Starting client..."
        exec $cmd
    else
        # Save to script
        local run_script="./run-client.sh"
        echo "#!/bin/bash" > "$run_script"
        echo "$cmd" >> "$run_script"
        chmod +x "$run_script"
        print_success "Saved run command to: $run_script"
    fi
}

show_help() {
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  server      Deploy and configure server"
    echo "  client      Deploy and configure client"
    echo "  download    Download pre-built binaries"
    echo "  build       Build binaries from source (requires Go)"
    echo "  genkey      Generate key pair only"
    echo "  help        Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0              # Interactive menu"
    echo "  $0 server       # Direct server deployment"
    echo "  $0 client       # Direct client deployment"
    echo "  $0 download     # Download binaries for your platform"
    echo "  $0 build        # Build from source"
    echo "  $0 genkey       # Generate keys"
}

main_menu() {
    print_banner

    echo "Select deployment mode:"
    echo ""
    echo "  1) Deploy Server"
    echo "  2) Deploy Client"
    echo "  3) Download Binaries Only"
    echo "  4) Build from Source"
    echo "  5) Generate Key Pair Only"
    echo "  6) Exit"
    echo ""
    echo -n "Choice [1-6]: "
    read -r choice

    case $choice in
        1)
            detect_platform
            get_binaries
            deploy_server
            ;;
        2)
            detect_platform
            get_binaries
            deploy_client
            ;;
        3)
            detect_platform
            download_binaries
            print_success "Download complete!"
            ;;
        4)
            build_binaries
            print_success "Build complete!"
            ;;
        5)
            detect_platform
            get_binaries
            echo -e "${BLUE}Enter directory for keys [default: ./keys]:${NC}"
            read -r key_dir
            key_dir=${key_dir:-./keys}
            generate_keys "$key_dir" "server"
            ;;
        6)
            echo "Goodbye!"
            exit 0
            ;;
        *)
            print_error "Invalid choice"
            exit 1
            ;;
    esac
}

# Main entry point
case "${1:-}" in
    server)
        print_banner
        detect_platform
        get_binaries
        deploy_server
        ;;
    client)
        print_banner
        detect_platform
        get_binaries
        deploy_client
        ;;
    download)
        print_banner
        detect_platform
        download_binaries
        print_success "Download complete!"
        ;;
    build)
        print_banner
        build_binaries
        print_success "Build complete!"
        ;;
    genkey)
        print_banner
        detect_platform
        get_binaries
        key_dir="${2:-./keys}"
        generate_keys "$key_dir" "server"
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        main_menu
        ;;
esac
