package source

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"
)

// AllowLocal lets what comes from a subscribed list be read from this machine or its network.
// It is only set by tests: a list is written by someone else, and neither its address nor the
// sources in it must be a way to reach services behind the server.
var AllowLocal bool

var errNotPublic = errors.New("not a public address")

// PublicOnly refuses connections to addresses that are not on the public internet. It runs on
// every connection, so a redirect to such an address is refused as well. A client that uses it
// must take no proxy from the environment: through one, the address checked would be the proxy's.
func PublicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if AllowLocal || ip != nil && !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || carrierNAT.Contains(ip)) {
		return nil
	}
	return &net.AddrError{Err: errNotPublic.Error(), Addr: host}
}

// carrierNAT is the shared address space of RFC 6598, which is not routed publicly either.
var carrierNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// PublicTransport connects directly and to public addresses only.
func PublicTransport() *http.Transport {
	return &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, Control: PublicOnly}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        16,
		IdleConnTimeout:     time.Minute,
	}
}
