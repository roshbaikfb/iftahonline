# ADR-0037: Where custom domains point (CNAME, ALIAS, A/AAAA)

- Status: Accepted
- Date: 2026-10-28

## Context

A custom domain is verified by a TXT record (ADR-0004), but the store
only opens on it once the domain's DNS points at the platform's edge.
The U6d domains screen told merchants to add a CNAME to the store's
platform subdomain. That fails for an apex domain (`example.com`,
`example.com.eg`): DNS does not allow a CNAME at a zone apex, and most
providers refuse one. Those domains need an ALIAS (ANAME, CNAME
flattening) where the provider has one, or A and AAAA records with the
edge's fixed addresses. The platform did not know those addresses.

The owner approved (2026-10-28) that the platform defines these records,
with one target for all custom domains, the addresses set by operators,
and IPv6 optional.

## Decision

1. **One target for every custom domain**, `CUSTOM_DOMAIN_TARGET`
   (default `connect.<PLATFORM_BASE_DOMAIN>`). Operators point that name
   at the edge. Unlike a store's subdomain, it does not depend on the
   store's slug, and the edge can move without merchants changing
   anything.
2. **The edge's fixed addresses come from the environment**:
   `CUSTOM_DOMAIN_IPV4` and `CUSTOM_DOMAIN_IPV6`, comma-separated, each
   a public global-unicast address of its family, validated at startup.
   Both are optional. The code never guesses them.
3. **Apex or not is decided by the public suffix list**
   (`golang.org/x/net/publicsuffix`): a host equal to its suffix plus one
   label is an apex. `example.com.eg` is an apex; `shop.example.com` is
   not.
4. **The API states the records.** Every custom domain carries
   `routing: {apex, records}`, an additive field in `/v1`:
   - a subdomain: one `CNAME` to the target;
   - an apex: an `ALIAS` to the target, then `A` and `AAAA` records for
     the configured addresses, in that order. With no addresses
     configured, the ALIAS stands alone and the dashboard suggests a
     `www` subdomain to merchants whose provider has no ALIAS.
   The platform subdomain has no `routing`; it is already ours.
5. **Verification is unchanged.** Ownership is still proven by TXT
   alone. Checking that the domain actually resolves to the edge is
   left for a later phase.

## Consequences

- Merchants with an apex domain get records that work at their
  provider. The dashboard shows ALIAS first, then A/AAAA, with copy
  buttons.
- Operators must keep `CUSTOM_DOMAIN_TARGET` pointing at the edge, and
  the addresses current. Changing the addresses means every apex
  domain using A/AAAA must be updated by its merchant. That is the
  reason ALIAS is offered first.
- The public suffix list is compiled into the binary and updates with
  `golang.org/x/net`.
- TLS for custom domains is still out of scope (docs/modules/tenancy.md).
