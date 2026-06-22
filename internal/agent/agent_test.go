package agent

import (
	"testing"
	"time"
)

func TestParseUpstreams(t *testing.T) {
	t.Parallel()

	parsed := ParseUpstreams([]string{"http://a, http://b", "https://c"})
	if len(parsed) != 3 {
		t.Fatalf("len(parsed) = %d, want 3", len(parsed))
	}
	if parsed[0] != "http://a" || parsed[1] != "http://b" || parsed[2] != "https://c" {
		t.Fatalf("unexpected upstreams: %+v", parsed)
	}
}

func TestParseHeartbeatResponse(t *testing.T) {
	t.Parallel()

	interval, err := parseHeartbeatResponse([]byte(`{"heartbeatInterval":"45s"}`))
	if err != nil {
		t.Fatalf("parseHeartbeatResponse() error = %v", err)
	}
	if interval != 45*time.Second {
		t.Fatalf("interval = %s, want 45s", interval)
	}
}

func TestParseHeartbeatResponseRejectsInvalidValue(t *testing.T) {
	t.Parallel()

	if _, err := parseHeartbeatResponse([]byte(`{"heartbeatInterval":"0s"}`)); err == nil {
		t.Fatal("parseHeartbeatResponse() error = nil, want error")
	}
}
