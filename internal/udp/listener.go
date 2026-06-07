// Package udp binds a UDP socket on all interfaces and forwards every
// well-sized datagram to a channel.
package udp

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync/atomic"

	"github.com/robbyronk/faster-together-collector/internal/forza"
)

// Listener reads from a UDP socket. Wrong-sized packets are counted but
// dropped; the first wrong-sized packet logs a friendly hint about Forza's
// "Data Out" format selection.
type Listener struct {
	conn          net.PacketConn
	WrongSize     atomic.Int64
	Total         atomic.Int64
	loggedHint    atomic.Bool
	loggedFirstOK atomic.Bool
}

// New binds 0.0.0.0:<port>. Returns the bound listener and the local
// address it ended up on (useful when port=0 is asked).
func New(port int) (*Listener, net.Addr, error) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, nil, err
	}
	return &Listener{conn: conn}, conn.LocalAddr(), nil
}

// Close releases the socket.
func (l *Listener) Close() error {
	if l.conn == nil {
		return nil
	}
	return l.conn.Close()
}

// Run reads from the socket and pushes every 324-byte payload onto out.
// Returns when ctx is canceled or the socket errors. The first valid
// packet logs "OK — receiving telemetry from <addr>" per PRD §2.2.
func (l *Listener) Run(ctx context.Context, out chan<- []byte) error {
	go func() {
		<-ctx.Done()
		_ = l.conn.Close()
	}()

	buf := make([]byte, 2048)
	for {
		n, addr, err := l.conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		l.Total.Add(1)

		if n != forza.PacketSize {
			l.WrongSize.Add(1)
			if l.loggedHint.CompareAndSwap(false, true) {
				log.Printf(
					"warning: received %d-byte packet from %s; expected %d. "+
						"Did you pick the 232-byte 'sled' format in Forza's Data Out settings? "+
						"Switch to the 'sled + dash' (324-byte) format.",
					n, addr, forza.PacketSize,
				)
			}
			continue
		}

		if l.loggedFirstOK.CompareAndSwap(false, true) {
			log.Printf("OK — receiving telemetry from %s", addr)
		}

		// Copy because we'll reuse `buf` on the next read.
		packet := make([]byte, n)
		copy(packet, buf[:n])

		select {
		case out <- packet:
		case <-ctx.Done():
			return nil
		}
	}
}
