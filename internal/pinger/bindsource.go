package pinger

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"
)

// sourceSockaddr constructs a source bind without losing a scoped IPv6 zone.
func sourceSockaddr(source string, v6 bool) (syscall.Sockaddr, error) {
	addr, err := netip.ParseAddr(source)
	if err != nil || addr.Unmap().Is6() != v6 {
		return nil, fmt.Errorf("invalid source %q for IPv6=%v", source, v6)
	}
	addr = addr.Unmap()
	if !v6 {
		return &syscall.SockaddrInet4{Addr: addr.As4()}, nil
	}
	var zone uint32
	if name := addr.Zone(); name != "" {
		n, err := strconv.ParseUint(name, 10, 32)
		if err == nil {
			zone = uint32(n)
		} else {
			iface, err := net.InterfaceByName(name)
			if err != nil {
				return nil, fmt.Errorf("source zone %s: %w", name, err)
			}
			zone = uint32(iface.Index)
		}
	}
	return &syscall.SockaddrInet6{Addr: addr.As16(), ZoneId: zone}, nil
}

var bindRawConnToSourceFn = func(raw syscall.RawConn, source string, v6 bool) error {
	addr, err := sourceSockaddr(source, v6)
	if err != nil {
		return err
	}
	var bindErr error
	if err := raw.Control(func(fd uintptr) { bindErr = bindSocketSource(fd, addr) }); err != nil {
		return err
	}
	return bindErr
}

// interfaceSource is used only when no explicit source was selected.
func (p *Pinger) interfaceSource(v6 bool) (string, error) {
	source := p.SourceV4
	if v6 {
		source = p.SourceV6
	}
	if source == "" {
		return "", fmt.Errorf("interface %s has no source address for IPv6=%v", p.Interface, v6)
	}
	return source, nil
}
