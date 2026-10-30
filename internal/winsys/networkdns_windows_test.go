package winsys

import "testing"

// NetworkDNS on the machine the tests run on: no error, and none of the addresses it
// leaves out.
func TestNetworkDNS(t *testing.T) {
	servers, err := NetworkDNS(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d servers", len(servers))
	for _, a := range servers {
		if a.IsLoopback() || a.IsUnspecified() || siteLocalDNS.Contains(a) {
			t.Errorf("%v is not left out", a)
		}
	}
}
