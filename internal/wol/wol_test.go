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
