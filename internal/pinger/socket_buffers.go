package pinger

import "fmt"

// A userspace receive slice is insufficient if the kernel drops the packet
// first. Darwin raw sockets default to 8192-byte send/receive queues, which
// can reject a loopback PMTU probe or drop its echo (including control data).
// Leave room for a maximum-size packet and kernel ancillary-data overhead.
func configureICMPBuffers(conn any) error {
	c, ok := conn.(interface {
		SetReadBuffer(int) error
		SetWriteBuffer(int) error
	})
	if !ok {
		return nil // Socket test doubles need not expose kernel buffer options.
	}
	const size = 2 * receiverBufferSize
	if err := c.SetReadBuffer(size); err != nil {
		return fmt.Errorf("set ICMP receive buffer: %w", err)
	}
	if err := c.SetWriteBuffer(size); err != nil {
		return fmt.Errorf("set ICMP send buffer: %w", err)
	}
	return nil
}
