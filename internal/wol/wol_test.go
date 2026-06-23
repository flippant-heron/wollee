package wol

import (
	"bytes"
	"testing"
)

func TestBuildMagicPacket(t *testing.T) {
	t.Parallel()

	packet, err := BuildMagicPacket("00:11:22:33:44:55")
	if err != nil {
		t.Fatalf("BuildMagicPacket() error = %v", err)
	}

	if len(packet) != 102 {
		t.Fatalf("packet length = %d, want 102", len(packet))
	}

	if !bytes.Equal(packet[:6], bytes.Repeat([]byte{0xff}, 6)) {
		t.Fatal("packet prefix does not contain sync stream")
	}

	mac := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	for offset := 6; offset < len(packet); offset += len(mac) {
		if !bytes.Equal(packet[offset:offset+len(mac)], mac) {
			t.Fatalf("packet block at offset %d does not match MAC", offset)
		}
	}
}

func TestNormalizeMACRejectsInvalidMAC(t *testing.T) {
	t.Parallel()

	if _, err := NormalizeMAC("bad-mac"); err == nil {
		t.Fatal("NormalizeMAC() error = nil, want error")
	}
}

func TestValidateBroadcastAcceptsCIDR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid CIDR /24", "192.168.1.0/24", false},
		{"valid CIDR /25", "10.0.0.0/25", false},
		{"valid CIDR /8", "172.16.0.0/8", false},
		{"valid IP", "192.168.1.255", false},
		{"valid IP", "10.0.0.1", false},
		{"invalid CIDR", "192.168.1.0/33", true},
		{"invalid IP", "999.999.999.999", true},
		{"empty string", "", true},
		{"invalid format", "not-an-ip", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBroadcast(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateBroadcast(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestCIDRToBroadcast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"CIDR /24", "192.168.1.0/24", "192.168.1.255", false},
		{"CIDR /25", "192.168.1.128/25", "192.168.1.255", false},
		{"CIDR /16", "192.168.0.0/16", "192.168.255.255", false},
		{"CIDR /8", "10.0.0.0/8", "10.255.255.255", false},
		{"plain IP", "192.168.1.255", "192.168.1.255", false},
		{"plain IP", "10.0.0.1", "10.0.0.1", false},
		{"invalid CIDR", "192.168.1.0/33", "", true},
		{"invalid IP", "not-an-ip", "", true},
		{"empty string", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CIDRToBroadcast(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CIDRToBroadcast(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("CIDRToBroadcast(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
