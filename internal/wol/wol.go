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

func ValidateBroadcast(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("must not be empty")
	}

	ip := net.ParseIP(raw)
	if ip == nil || ip.To4() == nil {
		return errors.New("must be a valid IPv4 address")
	}

	return nil
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

	udpAddr := &net.UDPAddr{
		IP:   net.ParseIP(broadcast),
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
