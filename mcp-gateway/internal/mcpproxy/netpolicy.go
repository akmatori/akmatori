package mcpproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// MaxResponseBytes caps the body the gateway reads from an external MCP server.
const MaxResponseBytes = 10 << 20 // 10 MiB

// ErrBlockedAddress is returned when an outbound MCP connection targets an
// address that the network policy refuses.
var ErrBlockedAddress = errors.New("destination address is not allowed")

// NetPolicy is the SSRF policy for outbound MCP HTTP traffic. The check runs
// on the resolved IP at connect time, so DNS answers cannot bypass it.
type NetPolicy struct {
	// AllowLoopback permits 127.0.0.0/8 and ::1 (dev and tests only).
	AllowLoopback bool
	// BlockPrivate refuses RFC 1918, CGNAT (100.64/10) and ULA (fc00::/7)
	// destinations. Off by default: self-hosted MCP servers often live there.
	BlockPrivate bool
}

// NetPolicyFromEnv reads MCP_ALLOW_LOOPBACK and MCP_BLOCK_PRIVATE_NETWORKS.
func NetPolicyFromEnv() NetPolicy {
	return NetPolicy{
		AllowLoopback: envBool("MCP_ALLOW_LOOPBACK"),
		BlockPrivate:  envBool("MCP_BLOCK_PRIVATE_NETWORKS"),
	}
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

var (
	cgnatPrefix     = netip.MustParsePrefix("100.64.0.0/10")
	thisNetPrefix   = netip.MustParsePrefix("0.0.0.0/8")
	broadcastV4Addr = netip.MustParseAddr("255.255.255.255")
)

// CheckIP reports whether the policy allows a connection to ip.
func (p NetPolicy) CheckIP(ip net.IP) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("%w: invalid IP", ErrBlockedAddress)
	}
	return p.checkAddr(addr)
}

func (p NetPolicy) checkAddr(addr netip.Addr) error {
	addr = addr.Unmap() // treat ::ffff:a.b.c.d as a.b.c.d
	blocked := func(reason string) error {
		return fmt.Errorf("%w: %s is %s", ErrBlockedAddress, addr, reason)
	}
	switch {
	case addr.IsLoopback():
		if !p.AllowLoopback {
			return blocked("a loopback address")
		}
		return nil
	case addr.IsUnspecified() || (addr.Is4() && thisNetPrefix.Contains(addr)):
		return blocked("an unspecified address")
	case addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast():
		return blocked("a link-local address")
	case addr.IsMulticast() || addr.IsInterfaceLocalMulticast() || addr == broadcastV4Addr:
		return blocked("a multicast or broadcast address")
	case p.BlockPrivate && (addr.IsPrivate() || cgnatPrefix.Contains(addr)):
		return blocked("a private network address (MCP_BLOCK_PRIVATE_NETWORKS=true)")
	}
	return nil
}

// ValidateURL checks that raw is an absolute http(s) URL with a host.
func (p NetPolicy) ValidateURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q: only http and https are allowed", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL has no host")
	}
	if u.User != nil {
		return fmt.Errorf("URL must not contain credentials; use auth_config")
	}
	// Literal IPs fail fast; host names are checked after DNS at dial time.
	if addr, err := netip.ParseAddr(u.Hostname()); err == nil {
		if err := p.checkAddr(addr); err != nil {
			return err
		}
	}
	return nil
}

// dialControl runs after DNS resolution, right before connect(2).
func (p NetPolicy) dialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBlockedAddress, err)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: unresolved address %q", ErrBlockedAddress, host)
	}
	return p.checkAddr(addr)
}

// errRedirect is returned by CheckRedirect: MCP endpoints must not redirect,
// because a redirect could move the request (and its auth header) elsewhere.
var errRedirect = errors.New("redirects are not allowed for MCP servers")

// NewHTTPClient returns an HTTP client that enforces the policy on every
// connection, refuses redirects and ignores proxy environment variables (a
// proxy would hide the real destination from the dial-time check).
func (p NetPolicy) NewHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   p.dialControl,
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirect
		},
	}
}

// errBodyTooLarge is returned when a response body exceeds MaxResponseBytes.
var errBodyTooLarge = fmt.Errorf("response body exceeds %d bytes", MaxResponseBytes)

// cappedReader returns an error (instead of a silent EOF) once more than
// limit bytes have been read.
type cappedReader struct {
	r     io.Reader
	limit int64
	read  int64
}

func newCappedReader(r io.Reader, limit int64) *cappedReader {
	return &cappedReader{r: r, limit: limit}
}

func (c *cappedReader) Read(b []byte) (int, error) {
	if c.read > c.limit {
		return 0, errBodyTooLarge
	}
	n, err := c.r.Read(b)
	c.read += int64(n)
	if c.read > c.limit {
		return n, errBodyTooLarge
	}
	return n, err
}
