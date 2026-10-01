package netavark

import "testing"

func TestDomainName(t *testing.T) {
	if got := DomainName("963ca160b022aabbccddeeff00112233"); got != "kt-963ca160b022" {
		t.Errorf("got %q", got)
	}
	if got := DomainName("abc"); got != "kt-abc" {
		t.Errorf("short id: got %q", got)
	}
}
