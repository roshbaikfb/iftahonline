# Architecture Decision Records

Format: Context, Decision, Consequences (Michael Nygard style). A record is
never edited after acceptance except to change its status; a new ADR
supersedes it.

| # | Title | Status |
|---|-------|--------|
| [0001](0001-go-modular-monolith-linux.md) | Go modular monolith, one binary, on Linux | Accepted |
| [0002](0002-shared-db-store-id-rls.md) | Shared database, `store_id` on every tenant table, RLS as second wall | Accepted |
| [0003](0003-river-jobs.md) | River (Postgres-backed) for background jobs and the outbox | Accepted |
| [0004](0004-host-resolution-cache.md) | Host → store resolution with a two-level cache | Accepted |
| [0005](0005-authentication.md) | Authentication: opaque sessions, argon2id, phone OTP, TOTP | Accepted |
| [0006](0006-authorization.md) | Authorization: roles, one permission matrix, 404 for non-members | Accepted |
| [0007](0007-cross-module-tx-tenant-jobs.md) | Cross-module transactions, tenant-scoped jobs, user-scoped RLS | Accepted |
| [0008](0008-session-lookup-in-postgres.md) | Session lookup in PostgreSQL, no Redis session cache (yet) | Accepted |
| [0009](0009-notifications-and-invitations.md) | Delivering codes and emails without a provider; invitation tokens | Accepted |
| [0010](0010-super-admin-console.md) | Super-admin console boundaries | Accepted |
| [0011](0011-catalog-and-stock-reservations.md) | Catalog data model and race-safe stock reservations | Accepted |
| [0012](0012-image-pipeline.md) | Product image pipeline: presigned uploads, re-encoded masters, signed imgproxy delivery | Accepted |
| [0013](0013-tenant-safe-search.md) | Tenant-safe product search with Arabic normalization | Accepted |
| [0014](0014-catalog-importers.md) | Catalog importers: CSV and WooCommerce as resumable jobs | Accepted |
| [0015](0015-domain-events-and-storefront-reads.md) | Domain events between modules; the catalog's storefront read side | Accepted |
| [0016](0016-storefront-themes-and-sections.md) | Storefront themes and pages built from schema-validated sections | Accepted |
| [0017](0017-store-assets-shared-image-pipeline.md) | Store assets (the logo) share the product image pipeline | Accepted |
| [0018](0018-storefront-rendering-and-caching.md) | Storefront rendering, caching and freshness | Accepted |
| [0019](0019-cart-edge-purge-and-browser-budgets.md) | Carts, edge purges, and browser budgets for the storefront | Accepted; §1 superseded by 0022 |
| [0020](0020-customers-order-stock-and-monthly-partitions.md) | Customers, stock held by orders, and one monthly partition job | Accepted |
| [0021](0021-orders-state-machine-idempotency-partitions.md) | Orders: state machine, idempotent placement, monthly partitions | Accepted; §8 superseded by 0025 |
| [0022](0022-redis-carts-one-step-orders-anti-fake.md) | Carts in Redis, one-step orders, and anti-fake checks | Accepted |
| [0023](0023-merchant-dashboard-shell.md) | The merchant dashboard's shell, and browser tests on the real API | Accepted |
| [0024](0024-inbound-webhooks-envelope-encryption-card-data.md) | Inbound webhooks, envelope-encrypted credentials, no card data | Accepted |
| [0025](0025-shipping-zones-rates-cod-fee.md) | Shipping zones, rates, free shipping and the COD fee | Accepted |
| [0026](0026-carriers-bosta-connections.md) | Carriers: one contract, a fake, Bosta, and sealed connections | Accepted |
| [0027](0027-shipments-tracking-order-moves.md) | Shipments: created by jobs, tracked by webhooks and polls, moving their orders | Accepted |
| [0028](0028-cod-reconciliation.md) | Cash-on-delivery reconciliation | Accepted |
| [0029](0029-card-payments-paymob.md) | Card payments: one gateway contract, a fake, Paymob, and the gateway's word | Accepted |
| [0030](0030-card-checkout-through-api-host.md) | Card checkout passes through the API host | Accepted |
| [0031](0031-p5-review-money-safety.md) | Money safety after the P5 review | Accepted |
| [0032](0032-extensible-section-types.md) | Section types are an extensible list | Accepted |
| [0033](0033-shop-search-and-bestsellers.md) | Shoppers' search key, and how bestsellers are counted | Accepted |
| [0034](0034-setup-wizard-and-starter-templates.md) | The setup wizard and starter templates | Accepted |
| [0035](0035-size-charts-and-option-colours.md) | Size charts and option colours | Accepted |
| [0036](0036-storefront-drafts-and-preview.md) | Storefront drafts, preview links, and checkout's starting method | Accepted |
| [0037](0037-custom-domain-routing-records.md) | Where custom domains point (CNAME, ALIAS, A/AAAA) | Accepted |
| [0038](0038-entitlements-platform-accounts-mailer.md) | Interim quotas, platform-owned messaging accounts, and a shared mailer | Accepted |
| [0039](0039-messaging-channels-and-log.md) | Messaging channels, the message log, and WhatsApp's word | Accepted |
| [0040](0040-order-confirmation-by-message.md) | Order confirmation by message | Accepted |
| [0041](0041-customer-started-confirmation.md) | Customers confirm on WhatsApp first, and are thanked | Accepted |
| [0042](0042-coupons.md) | Coupons | Accepted |
| [0043](0043-ad-pixels-and-conversions.md) | Ad pixels, consent, and server-side conversions | Accepted |
| [0044](0044-product-feeds.md) | Product feeds for Google and Meta | Accepted |
| [0045](0045-plans-as-data-and-one-entitlements-service.md) | Plans as data, and one entitlements service | Accepted |
| [0046](0046-enforcing-plan-limits.md) | Enforcing plan limits | Accepted |
| [0047](0047-plan-features-where-they-are-used.md) | Plan features are checked where they are used | Accepted |
| [0048](0048-pay-as-you-sell-fees.md) | Pay-as-you-sell fees | Accepted |
| [0049](0049-billing-accounts-and-monthly-invoices.md) | Billing accounts and monthly invoices | Accepted |
| [0050](0050-plan-changes-reach-every-cache.md) | Plan changes reach every cache | Accepted |
| [0051](0051-collection-and-dunning.md) | Collecting invoices, dunning, and the billing hold | Accepted |
| [0052](0052-charging-cards-on-file-with-paymob.md) | Charging merchants' cards on file with Paymob | Accepted |
| [0053](0053-paying-invoices-and-saving-the-card.md) | Paying an invoice on Paymob's page, and saving the card | Accepted |
| [0054](0054-owners-billing-notices-on-whatsapp.md) | Owners' billing notices on WhatsApp | Accepted |
| [0055](0055-time-limited-audited-impersonation.md) | Time-limited, audited impersonation | Accepted |
