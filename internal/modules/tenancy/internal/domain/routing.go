package domain

import (
	"net/netip"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Edge is where custom domains point (ADR-0037): one host name for all of
// them, and the fixed addresses behind it for apex domains whose DNS
// provider has no ALIAS. The addresses come from the operators' config;
// none configured means an apex domain is told to use an ALIAS.
type Edge struct {
	Target string
	IPv4   []netip.Addr
	IPv6   []netip.Addr
}

// DNSRecord is one record the merchant adds at their DNS provider.
type DNSRecord struct {
	Type  string // CNAME, ALIAS, A or AAAA
	Name  string
	Value string
}

// Routing is what a custom domain needs to reach the store.
type Routing struct {
	Apex    bool
	Records []DNSRecord
}

// IsApex says whether host is a registrable root (example.com,
// example.com.eg): the public suffix list's suffix plus one label. A
// public suffix itself, or a name below the root, is not.
func IsApex(host string) bool {
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return false
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(host)
	return err == nil && root == host
}

// RoutingFor is the records for host: one CNAME for a subdomain; for an
// apex, an ALIAS where the provider has one, else the A and AAAA records.
func (e Edge) RoutingFor(host string) Routing {
	if !IsApex(host) {
		return Routing{Records: []DNSRecord{{Type: "CNAME", Name: host, Value: e.Target}}}
	}
	out := Routing{Apex: true, Records: make([]DNSRecord, 0, 1+len(e.IPv4)+len(e.IPv6))}
	out.Records = append(out.Records, DNSRecord{Type: "ALIAS", Name: host, Value: e.Target})
	for _, a := range e.IPv4 {
		out.Records = append(out.Records, DNSRecord{Type: "A", Name: host, Value: a.String()})
	}
	for _, a := range e.IPv6 {
		out.Records = append(out.Records, DNSRecord{Type: "AAAA", Name: host, Value: a.String()})
	}
	return out
}
