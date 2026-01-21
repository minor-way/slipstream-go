package proxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// SOCKS5 constants per RFC 1928
const (
	SOCKS5Version = 0x05

	// Authentication methods
	AuthNone         = 0x00
	AuthUserPassword = 0x02
	AuthNoAcceptable = 0xFF

	// Commands
	CmdConnect = 0x01

	// Address types
	AddrTypeIPv4   = 0x01
	AddrTypeDomain = 0x03
	AddrTypeIPv6   = 0x04

	// Reply codes
	ReplySuccess             = 0x00
	ReplyGeneralFailure      = 0x01
	ReplyConnectionNotAllowed = 0x02
	ReplyNetworkUnreachable  = 0x03
	ReplyHostUnreachable     = 0x04
	ReplyConnectionRefused   = 0x05
	ReplyTTLExpired          = 0x06
	ReplyCommandNotSupported = 0x07
	ReplyAddressNotSupported = 0x08
)

// SOCKS5Dialer implements a SOCKS5 client dialer
type SOCKS5Dialer struct {
	ProxyAddr string
	Username  string
	Password  string
}

// NewSOCKS5Dialer creates a new SOCKS5 dialer
func NewSOCKS5Dialer(proxyAddr string) *SOCKS5Dialer {
	return &SOCKS5Dialer{ProxyAddr: proxyAddr}
}

// NewSOCKS5DialerWithAuth creates a new SOCKS5 dialer with authentication
func NewSOCKS5DialerWithAuth(proxyAddr, username, password string) *SOCKS5Dialer {
	return &SOCKS5Dialer{
		ProxyAddr: proxyAddr,
		Username:  username,
		Password:  password,
	}
}

// Dial connects to the target address through the SOCKS5 proxy
func (d *SOCKS5Dialer) Dial(network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("socks5: only TCP is supported")
	}

	// Connect to proxy
	conn, err := net.Dial("tcp", d.ProxyAddr)
	if err != nil {
		return nil, fmt.Errorf("socks5: connect to proxy: %w", err)
	}

	// Perform handshake
	if err := d.handshake(conn); err != nil {
		conn.Close()
		return nil, err
	}

	// Send CONNECT request
	if err := d.connect(conn, addr); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

// handshake performs SOCKS5 greeting and authentication
func (d *SOCKS5Dialer) handshake(conn net.Conn) error {
	// Greeting: version, num methods, methods
	var methods []byte
	if d.Username != "" {
		methods = []byte{AuthNone, AuthUserPassword}
	} else {
		methods = []byte{AuthNone}
	}

	greeting := make([]byte, 2+len(methods))
	greeting[0] = SOCKS5Version
	greeting[1] = byte(len(methods))
	copy(greeting[2:], methods)

	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("socks5: send greeting: %w", err)
	}

	// Read server choice
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return fmt.Errorf("socks5: read greeting response: %w", err)
	}

	if resp[0] != SOCKS5Version {
		return fmt.Errorf("socks5: unexpected version %d", resp[0])
	}

	switch resp[1] {
	case AuthNone:
		return nil
	case AuthUserPassword:
		return d.authenticateUserPassword(conn)
	case AuthNoAcceptable:
		return errors.New("socks5: no acceptable authentication method")
	default:
		return fmt.Errorf("socks5: unexpected auth method %d", resp[1])
	}
}

// authenticateUserPassword performs username/password authentication (RFC 1929)
func (d *SOCKS5Dialer) authenticateUserPassword(conn net.Conn) error {
	if d.Username == "" {
		return errors.New("socks5: username required but not provided")
	}

	// Auth request: version, ulen, username, plen, password
	authReq := make([]byte, 3+len(d.Username)+len(d.Password))
	authReq[0] = 0x01 // subnegotiation version
	authReq[1] = byte(len(d.Username))
	copy(authReq[2:], d.Username)
	authReq[2+len(d.Username)] = byte(len(d.Password))
	copy(authReq[3+len(d.Username):], d.Password)

	if _, err := conn.Write(authReq); err != nil {
		return fmt.Errorf("socks5: send auth: %w", err)
	}

	// Read auth response
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return fmt.Errorf("socks5: read auth response: %w", err)
	}

	if resp[1] != 0x00 {
		return errors.New("socks5: authentication failed")
	}

	return nil
}

// connect sends a CONNECT request to the SOCKS5 proxy
func (d *SOCKS5Dialer) connect(conn net.Conn, addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("socks5: invalid address: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("socks5: invalid port: %w", err)
	}

	// Build CONNECT request
	req := []byte{SOCKS5Version, CmdConnect, 0x00} // version, cmd, reserved

	// Append address
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, AddrTypeIPv4)
			req = append(req, ip4...)
		} else {
			req = append(req, AddrTypeIPv6)
			req = append(req, ip...)
		}
	} else {
		// Domain name
		if len(host) > 255 {
			return errors.New("socks5: domain name too long")
		}
		req = append(req, AddrTypeDomain, byte(len(host)))
		req = append(req, host...)
	}

	// Append port (big endian)
	req = append(req, byte(port>>8), byte(port))

	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("socks5: send connect: %w", err)
	}

	// Read response header
	resp := make([]byte, 4)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return fmt.Errorf("socks5: read connect response: %w", err)
	}

	if resp[0] != SOCKS5Version {
		return fmt.Errorf("socks5: unexpected version %d in response", resp[0])
	}

	if resp[1] != ReplySuccess {
		return fmt.Errorf("socks5: connect failed with code %d: %s", resp[1], replyCodeToString(resp[1]))
	}

	// Read and discard bound address (we don't need it)
	switch resp[3] {
	case AddrTypeIPv4:
		if _, err := io.ReadFull(conn, make([]byte, 4+2)); err != nil { // IPv4 + port
			return err
		}
	case AddrTypeDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, make([]byte, int(lenBuf[0])+2)); err != nil { // domain + port
			return err
		}
	case AddrTypeIPv6:
		if _, err := io.ReadFull(conn, make([]byte, 16+2)); err != nil { // IPv6 + port
			return err
		}
	}

	return nil
}

func replyCodeToString(code byte) string {
	switch code {
	case ReplySuccess:
		return "success"
	case ReplyGeneralFailure:
		return "general failure"
	case ReplyConnectionNotAllowed:
		return "connection not allowed"
	case ReplyNetworkUnreachable:
		return "network unreachable"
	case ReplyHostUnreachable:
		return "host unreachable"
	case ReplyConnectionRefused:
		return "connection refused"
	case ReplyTTLExpired:
		return "TTL expired"
	case ReplyCommandNotSupported:
		return "command not supported"
	case ReplyAddressNotSupported:
		return "address not supported"
	default:
		return "unknown"
	}
}

// ParseTargetAddress parses a SOCKS5-style address from a reader
// Format: [1 byte type][address][2 bytes port BE]
func ParseTargetAddress(r io.Reader) (string, error) {
	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(r, typeBuf); err != nil {
		return "", fmt.Errorf("read address type: %w", err)
	}

	var host string
	switch typeBuf[0] {
	case AddrTypeIPv4:
		ipBuf := make([]byte, 4)
		if _, err := io.ReadFull(r, ipBuf); err != nil {
			return "", fmt.Errorf("read IPv4: %w", err)
		}
		host = net.IP(ipBuf).String()

	case AddrTypeDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			return "", fmt.Errorf("read domain length: %w", err)
		}
		domainBuf := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(r, domainBuf); err != nil {
			return "", fmt.Errorf("read domain: %w", err)
		}
		host = string(domainBuf)

	case AddrTypeIPv6:
		ipBuf := make([]byte, 16)
		if _, err := io.ReadFull(r, ipBuf); err != nil {
			return "", fmt.Errorf("read IPv6: %w", err)
		}
		host = net.IP(ipBuf).String()

	default:
		return "", fmt.Errorf("unknown address type: %d", typeBuf[0])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(r, portBuf); err != nil {
		return "", fmt.Errorf("read port: %w", err)
	}
	port := binary.BigEndian.Uint16(portBuf)

	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// WriteTargetAddress writes a target address in SOCKS5 format
// Format: [1 byte type][address][2 bytes port BE]
func WriteTargetAddress(w io.Writer, addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse address: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("parse port: %w", err)
	}

	var buf []byte

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			buf = append(buf, AddrTypeIPv4)
			buf = append(buf, ip4...)
		} else {
			buf = append(buf, AddrTypeIPv6)
			buf = append(buf, ip...)
		}
	} else {
		// Domain name
		if len(host) > 255 {
			return errors.New("domain name too long")
		}
		buf = append(buf, AddrTypeDomain, byte(len(host)))
		buf = append(buf, host...)
	}

	// Port in big endian
	buf = append(buf, byte(port>>8), byte(port))

	_, err = w.Write(buf)
	return err
}
