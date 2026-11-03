package core

import (
	"net"
	"net/http"
	"strings"
)

// Security group modes used by web rules and port forwarding.
const (
	GroupModeNone       = ""           // security group not involved
	GroupModeStrict     = "strict"     // IP filter AND group authorization
	GroupModeSupplement = "supplement" // IP filter OR group authorization
)

// CheckAccess combines an IP filter list with security group authorization.
func (a *App) CheckAccess(ip net.IP, listID, groupID, mode string) bool {
	filterOK := true
	if listID != "" && a.IPFilter != nil {
		filterOK = a.IPFilter.Allow(listID, ip)
	}
	if groupID == "" || mode == GroupModeNone || a.Groups == nil {
		return filterOK
	}
	auth := a.Groups.IsAuthorized(groupID, ip.String())
	if mode == GroupModeSupplement {
		return filterOK || auth
	}
	return filterOK && auth
}

// HostIP extracts the IP of a "host:port" address.
func HostIP(addr string) net.IP {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return ip
}

// RequestIP returns the TCP peer IP of a request.
func RequestIP(r *http.Request) net.IP { return HostIP(r.RemoteAddr) }

// cgnat is the 100.64.0.0/10 shared address space (used by Tailscale and
// carrier-grade NAT), treated as "LAN" for admin access checks.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// IsLAN reports loopback / private / link-local / CGNAT addresses.
func IsLAN(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && cgnat.Contains(v4) {
		return true
	}
	return false
}
