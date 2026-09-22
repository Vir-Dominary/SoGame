package natdetect

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"time"
)

const (
	stunMagicCookie uint32 = 0x2112A442
	stunBindingReq  uint16 = 0x0001
	stunBindingResp uint16 = 0x0101
	attrXorMapped   uint16 = 0x0020
	attrMappedAddr  uint16 = 0x0001
	stunTimeout            = 3 * time.Second
)

type mappedAddr struct {
	IP   net.IP
	Port int
}

func buildBindingRequest() ([]byte, error) {
	msg := make([]byte, 20)
	binary.BigEndian.PutUint16(msg[0:2], stunBindingReq)
	binary.BigEndian.PutUint16(msg[2:4], 0)
	binary.BigEndian.PutUint32(msg[4:8], stunMagicCookie)
	_, err := rand.Read(msg[8:20])
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func sendSTUN(server string) (mappedAddr, error) {
	addr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return mappedAddr{}, err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return mappedAddr{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(stunTimeout))
	req, err := buildBindingRequest()
	if err != nil {
		return mappedAddr{}, err
	}
	if _, err := conn.Write(req); err != nil {
		return mappedAddr{}, err
	}
	resp := make([]byte, 2048)
	n, _, err := conn.ReadFromUDP(resp)
	if err != nil {
		return mappedAddr{}, err
	}
	return parseResponse(resp[:n])
}

func parseResponse(msg []byte) (mappedAddr, error) {
	if len(msg) < 20 {
		return mappedAddr{}, errInvalidSTUN
	}
	msgType := binary.BigEndian.Uint16(msg[0:2])
	if msgType != stunBindingResp {
		return mappedAddr{}, errInvalidSTUN
	}
	msgLen := binary.BigEndian.Uint16(msg[2:4])
	if len(msg) < int(20+msgLen) {
		return mappedAddr{}, errInvalidSTUN
	}
	cookie := binary.BigEndian.Uint32(msg[4:8])
	if cookie != stunMagicCookie {
		return mappedAddr{}, errInvalidSTUN
	}
	offset := 20
	for offset+4 <= len(msg) {
		attrType := binary.BigEndian.Uint16(msg[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(msg[offset+2 : offset+4]))
		attrStart := offset + 4
		if attrStart+attrLen > len(msg) {
			break
		}
		if attrType == attrXorMapped || attrType == attrMappedAddr {
			return parseMappedAddress(msg[attrStart:attrStart+attrLen], attrType == attrXorMapped, msg[4:8])
		}
		padded := (attrLen + 3) &^ 3
		offset += 4 + padded
	}
	return mappedAddr{}, errNoMappedAddr
}

func parseMappedAddress(data []byte, xor bool, cookie []byte) (mappedAddr, error) {
	if len(data) < 8 {
		return mappedAddr{}, errInvalidSTUN
	}
	family := data[1]
	port := int(binary.BigEndian.Uint16(data[2:4]))
	if xor {
		port ^= int(binary.BigEndian.Uint16(cookie[0:2]))
	}
	var ip net.IP
	if family == 0x01 {
		if len(data) < 8 {
			return mappedAddr{}, errInvalidSTUN
		}
		rawIP := data[4:8]
		if xor {
			xorIP := make([]byte, 4)
			xorVal := binary.BigEndian.Uint32(cookie)
			binary.BigEndian.PutUint32(xorIP, binary.BigEndian.Uint32(rawIP)^xorVal)
			ip = net.IP(xorIP)
		} else {
			ip = net.IP(rawIP)
		}
	} else {
		return mappedAddr{}, errUnsupportedFamily
	}
	return mappedAddr{IP: ip, Port: port}, nil
}

var (
	errInvalidSTUN      = errSTUN("invalid STUN response")
	errNoMappedAddr     = errSTUN("no mapped address in STUN response")
	errUnsupportedFamily = errSTUN("unsupported address family")
)

type errSTUN string

func (e errSTUN) Error() string { return string(e) }
