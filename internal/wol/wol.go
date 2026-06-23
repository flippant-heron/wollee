package wol

import (
	"errors"
	"fmt"
	"net"
	"strings"

	bmp "github.com/ahiggins0/go-wol/wol"
)

func NormalizeMAC(raw string) (string, error) {
	hwAddr, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse MAC address: %w", err)
	}

	if len(hwAddr) != 6 {
		return "", errors.New("must be a 48-bit MAC address")
	}

	return strings.ToLower(hwAddr.String()), nil
}

// ValidateBroadcast validates that the input is either a valid CIDR notation or a broadcast IP address.
func ValidateBroadcast(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("must not be empty")
	}

	// Try parsing as CIDR first
	if _, _, err := net.ParseCIDR(raw); err == nil {
		return nil // Valid CIDR
	}

	// Fall back to parsing as IP address
	ip := net.ParseIP(raw)
	if ip == nil || ip.To4() == nil {
		return errors.New("must be a valid CIDR notation (e.g., 192.168.1.0/24) or IPv4 address")
	}

	return nil
}

// CIDRToBroadcast converts a CIDR notation to a broadcast address.
// If a plain IP address is provided, it is returned as-is.
func CIDRToBroadcast(cidr string) (string, error) {
	// Try parsing as CIDR first
	_, ipnet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err == nil {
		// Valid CIDR - calculate broadcast address
		broadcast := net.IP(make([]byte, 4))
		for i := 0; i < 4; i++ {
			broadcast[i] = ipnet.IP[i] | ^ipnet.Mask[i]
		}
		return broadcast.String(), nil
	}

	// Try parsing as plain IP
	plainIP := net.ParseIP(strings.TrimSpace(cidr))
	if plainIP != nil && plainIP.To4() != nil {
		return plainIP.String(), nil
	}

	return "", errors.New("invalid CIDR or IP address")
}

func BuildMagicPacket(mac string) ([]byte, error) {
	normalizedMAC, err := NormalizeMAC(mac)
	if err != nil {
		return nil, err
	}

	packet, err := bmp.New(normalizedMAC)
	if err != nil {
		return nil, fmt.Errorf("create magic packet: %w", err)
	}

	payload, err := packet.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal magic packet: %w", err)
	}

	return payload, nil
}

func SendMagicPacket(mac string, broadcast string) error {
	payload, err := BuildMagicPacket(mac)
	if err != nil {
		return err
	}

	if err := ValidateBroadcast(broadcast); err != nil {
		return fmt.Errorf("validate broadcast address: %w", err)
	}

	// Convert CIDR to broadcast address if needed
	broadcastAddr, err := CIDRToBroadcast(broadcast)
	if err != nil {
		return fmt.Errorf("convert CIDR to broadcast: %w", err)
	}

	udpAddr := &net.UDPAddr{
		IP:   net.ParseIP(broadcastAddr),
		Port: 9,
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("dial udp broadcast address: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("write magic packet: %w", err)
	}

	return nil
}
