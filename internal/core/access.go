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

// IsLAN reports loopback/private/link-local addresses.
func IsLAN(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}
