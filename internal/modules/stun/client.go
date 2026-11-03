package stun

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"time"
)

// Minimal STUN (RFC 5389) binding client: sends a Binding Request and parses
// the XOR-MAPPED-ADDRESS from the response to learn the public mapping of a
// local UDP socket.

const magicCookie = 0x2112A442

type mapped struct {
	IP   net.IP
	Port int
}

func bindingRequest(conn *net.UDPConn, server *net.UDPAddr, timeout time.Duration) (*mapped, error) {
	var txID [12]byte
	if _, err := rand.Read(txID[:]); err != nil {
		return nil, err
	}
	msg := make([]byte, 20)
	binary.BigEndian.PutUint16(msg[0:], 0x0001) // Binding Request
	binary.BigEndian.PutUint16(msg[2:], 0)      // length
	binary.BigEndian.PutUint32(msg[4:], magicCookie)
	copy(msg[8:], txID[:])

	if _, err := conn.WriteToUDP(msg, server); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1024)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return nil, err
		}
		if n < 20 {
			continue
		}
		if binary.BigEndian.Uint16(buf[0:]) != 0x0101 { // Binding Success
			continue
		}
		if binary.BigEndian.Uint32(buf[4:]) != magicCookie {
			continue
		}
		m := parseAttrs(buf[20:n], txID)
		if m != nil {
			return m, nil
		}
	}
}

func parseAttrs(b []byte, txID [12]byte) *mapped {
	for len(b) >= 4 {
		typ := binary.BigEndian.Uint16(b[0:])
		ln := int(binary.BigEndian.Uint16(b[2:]))
		if 4+ln > len(b) {
			break
		}
		val := b[4 : 4+ln]
		switch typ {
		case 0x0020: // XOR-MAPPED-ADDRESS
			if m := parseXorAddr(val, txID); m != nil {
				return m
			}
		case 0x0001: // MAPPED-ADDRESS (fallback)
			if m := parseAddr(val); m != nil {
				return m
			}
		}
		adv := 4 + ln
		if pad := ln % 4; pad != 0 {
			adv += 4 - pad
		}
		b = b[adv:]
	}
	return nil
}

func parseXorAddr(val []byte, txID [12]byte) *mapped {
	if len(val) < 8 {
		return nil
	}
	fam := val[1]
	port := binary.BigEndian.Uint16(val[2:]) ^ uint16(magicCookie>>16)
	if fam == 0x01 {
		ip := make(net.IP, 4)
		cookie := []byte{0x21, 0x12, 0xA4, 0x42}
		for i := 0; i < 4; i++ {
			ip[i] = val[4+i] ^ cookie[i]
		}
		return &mapped{IP: ip, Port: int(port)}
	}
	if fam == 0x02 && len(val) >= 20 {
		ip := make(net.IP, 16)
		key := append([]byte{0x21, 0x12, 0xA4, 0x42}, txID[:]...)
		for i := 0; i < 16; i++ {
			ip[i] = val[4+i] ^ key[i]
		}
		return &mapped{IP: ip, Port: int(port)}
	}
	return nil
}

func parseAddr(val []byte) *mapped {
	if len(val) < 8 || val[1] != 0x01 {
		return nil
	}
	return &mapped{IP: net.IP(val[4:8]), Port: int(binary.BigEndian.Uint16(val[2:]))}
}

func resolveServer(addr string) (*net.UDPAddr, error) {
	if addr == "" {
		return nil, errors.New("empty STUN server")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "3478")
	}
	return net.ResolveUDPAddr("udp", addr)
}
