package server

import (
	"errors"
	"net"
	"time"

	"slipstream-go/internal/protocol"
)

// VirtualConn bridges the gap between DNS and QUIC.
// It implements net.PacketConn so QUIC thinks it's a UDP socket.
type VirtualConn struct {
	Sessions *SessionManager
	// Incoming is where reassembled packets from DNSHandler are waiting
	// to be read by the QUIC listener.
	Incoming chan PacketBundle
}

type PacketBundle struct {
	Data []byte
	Addr net.Addr
}

func NewVirtualConn(sm *SessionManager) *VirtualConn {
	return &VirtualConn{
		Sessions: sm,
		Incoming: make(chan PacketBundle, 1000),
	}
}

// InjectPacket is called by DNSHandler when a full packet is reassembled.
func (vc *VirtualConn) InjectPacket(data []byte, sessionID string) {
	addr := &SessionAddr{SessionID: sessionID}
	select {
	case vc.Incoming <- PacketBundle{Data: data, Addr: addr}:
	default:
		// Drop if full
	}
}

// --- net.PacketConn Implementation ---

// ReadFrom: Called by QUIC to get data. We return data from our channel.
func (vc *VirtualConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	bundle := <-vc.Incoming
	n = copy(p, bundle.Data)
	return n, bundle.Addr, nil
}

// WriteTo: Called by QUIC to send data. Pre-fragment and queue for DNS fetching.
func (vc *VirtualConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	sessAddr, ok := addr.(*SessionAddr)
	if !ok {
		return 0, errors.New("invalid address type")
	}

	sess := vc.Sessions.GetOrCreate(sessAddr.SessionID)

	// Fragment the packet before queueing
	fragments := protocol.FragmentPacket(p)

	// Queue all fragments
	for _, frag := range fragments {
		select {
		case sess.FragQueue <- frag:
		default:
			// Drop if full (Congestion Control)
			return 0, nil
		}
	}

	return len(p), nil
}

// Close: Required by interface
func (vc *VirtualConn) Close() error { return nil }

// LocalAddr: Required by interface (Spoofing UDP)
func (vc *VirtualConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}
}

// Deadlines: Required by interface
func (vc *VirtualConn) SetDeadline(t time.Time) error      { return nil }
func (vc *VirtualConn) SetReadDeadline(t time.Time) error  { return nil }
func (vc *VirtualConn) SetWriteDeadline(t time.Time) error { return nil }

// --- Custom Address Type ---

type SessionAddr struct {
	SessionID string
}

func (a *SessionAddr) Network() string { return "udp" } // Must say "udp" for QUIC
func (a *SessionAddr) String() string  { return a.SessionID }
