package scan

import (
	"net"
	"sort"
)

const maxHostsPerNet = 65534

// Hosts returns usable IPs in the CIDR, skipping the network and broadcast
// addresses for IPv4. Networks that would exceed maxHostsPerNet are skipped
// (the gateway should be given tighter prefixes, typically /16 or smaller).
func Hosts(n *net.IPNet) []net.IP {
	if n == nil {
		return nil
	}
	ones, bits := n.Mask.Size()
	if bits-ones > 16 {
		return nil
	}
	count := 1 << uint(bits-ones)
	if count > maxHostsPerNet+2 {
		return nil
	}
	base := n.IP.To4()
	ipv4 := true
	if base == nil {
		base = n.IP.To16()
		ipv4 = false
	}
	if base == nil {
		return nil
	}

	out := make([]net.IP, 0, count)
	ip := append(net.IP(nil), base...)
	for i := 0; i < count; i++ {
		cur := append(net.IP(nil), ip...)
		if ipv4 && (i == 0 || i == count-1) && ones < 31 {
			inc(ip)
			continue
		}
		if n.Contains(cur) {
			out = append(out, cur)
		}
		inc(ip)
	}
	return out
}

func inc(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			return
		}
	}
}

// UniqueIPs merges and de-duplicates IP lists.
func UniqueIPs(lists ...[]net.IP) []net.IP {
	seen := map[string]net.IP{}
	for _, list := range lists {
		for _, ip := range list {
			seen[ip.String()] = ip
		}
	}
	out := make([]net.IP, 0, len(seen))
	for _, ip := range seen {
		out = append(out, ip)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
