package resource

import ma "github.com/multiformats/go-multiaddr"

// CircuitHops counts /p2p-circuit components in an address.
func CircuitHops(addr ma.Multiaddr) int {
	if addr == nil {
		return 0
	}
	n := 0
	for _, p := range addr.Protocols() {
		if p.Code == ma.P_CIRCUIT {
			n++
		}
	}
	return n
}
