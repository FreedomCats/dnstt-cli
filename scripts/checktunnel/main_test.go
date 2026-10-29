//go:build windows

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunOffline runs the snapshot on the real system without the network: all sections
// are there, the network ones are not. Without administrator rights the WFP section has
// an error, which is fine.
func TestRunOffline(t *testing.T) {
	var b bytes.Buffer
	run(&b, options{label: "test", name: "dnstt-cli", offline: true})
	out := b.String()
	for _, s := range []string{"— test ===", "-- dnstt-cli.exe processes", "-- Adapters", "-- Routes /0 and /1",
		"-- WFP: DNS protection", "-- Registry", "NRPT", "{3307B858-2357-4C8A-8349-A5F7FD1A0255}"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
	for _, s := range []string{"External address", "Resolver"} {
		if strings.Contains(out, s) {
			t.Errorf("with offline the output has %q", s)
		}
	}
	t.Log("\n" + out)
}
