# Before going live

The platform runs end to end on fakes and stand-ins. Some services need
real accounts, and some settings need values, before real stores and
shoppers use it. This page lists them, in order. The server settings are
environment variables (see `internal/platform/config` and the examples in
`deploy/systemd/env/`). Every secret comes from the secret manager, never
from a file in the repository.

Status as of P6 (2026-10-29). Each item says what happens if it is
missing: most features switch themselves off rather than fail.

## 1. WhatsApp (order confirmation)

Without it, cash-on-delivery orders still arrive, but no confirmation
message is sent. The merchant confirms them by hand (or by SMS, if
section 2 is done).

1. **A Meta business and app.** Create a Meta Business account, verify
   the business, and create an app with the WhatsApp product.
2. **The platform's number.** Add and verify the WhatsApp number that
   sends for every store (ADR-0039). Its display name is the platform's;
   each message names the store.
3. **The template.** In WhatsApp Manager, on that number, create
   `order_confirmation_v1` and wait for Meta's approval (hours to days):
   - category Utility; languages Arabic (`ar`) and English (`en`);
   - body variables `{{1}}` the customer's name, `{{2}}` the store's name,
     `{{3}}` the order reference, `{{4}}` the total;
   - two quick-reply buttons, in this order: «تأكيد الطلب» then «إلغاء
     الطلب» (English: "Confirm order" then "Cancel order").

   The exact texts are in [modules/messaging.md](modules/messaging.md).
4. **A permanent token.** Create a system user with access to the app and
   the WhatsApp account, and give it a permanent token
   (`whatsapp_business_messaging`). A temporary token expires in a day.
5. **The webhook.** In the app's WhatsApp configuration:
   - set the callback URL to `https://app.iftahonline.com/v1/webhooks/whatsapp`
     and the verify token to `WHATSAPP_VERIFY_TOKEN`;
   - subscribe to the `messages` field (delivery statuses and the
     customers' answers).

   Every delivery is checked against `WHATSAPP_APP_SECRET`.
6. **The settings** (api and worker; the storefront takes the display
   number only):

   | Variable | Value |
   |---|---|
   | `WHATSAPP_PHONE_NUMBER_ID` | The number's ID in the app (digits) |
   | `WHATSAPP_ACCESS_TOKEN` | The system user's permanent token (secret) |
   | `WHATSAPP_APP_SECRET` | The app's secret (secret) |
   | `WHATSAPP_VERIFY_TOKEN` | A random string you choose (secret) |
   | `WHATSAPP_DISPLAY_NUMBER` | The number in E.164, `+20…` (public); also on the storefront, for the thank-you page's "confirm on WhatsApp" button |

   The first four go together: the service refuses to start with only
   some of them.
7. **Billing.** Utility conversations are charged to the app's account.
   A conversation the customer starts is free; that is what the "confirm
   on WhatsApp" button does (ADR-0041).

## 2. SMS (the fallback)

Without it, a customer WhatsApp cannot reach gets no message; the merchant
is told to call them.

1. **A Twilio account** with a sender for Egypt and the Gulf. Use one of:
   a number, an alphanumeric sender ID (register it where the country
   requires), or a Messaging Service (`MG…`).
2. **The settings** (api and worker):

   | Variable | Value |
   |---|---|
   | `TWILIO_ACCOUNT_SID` | Starts with `AC` |
   | `TWILIO_AUTH_TOKEN` | Secret |
   | `TWILIO_FROM` | The number, sender ID or `MG…` service |

   The three go together. Twilio's status callback is set per message
   (`/v1/webhooks/twilio`); nothing to configure in Twilio's console.

## 3. Plans, prices and quotas

Plans are data (ADR-0045, [billing](modules/billing.md)). Migration 00056
seeds six plans whose **prices and limits are placeholders**:

- Set the real prices per currency, and each plan's limits and features,
  before launch from the console: publish a new version of each plan
  (`POST /admin/v1/plans`, ADR-0056).
- The `messages` grant is the platform's cost per store for WhatsApp and
  SMS. The free plan's is 0.
- Merchants are warned at 80% and at the limit, and orders are never
  blocked by a quota.
- Existing stores got a 14-day trial of pro when 00056 ran.

`ENTITLEMENTS_MESSAGES_PER_MONTH` is gone; remove it from the env files.

### Collecting invoices

- **`BILLING_COLLECTION_ENABLED`** (worker; default `false`) turns on
  the hourly dunning: charges, retries, suspensions (ADR-0051). Leave it
  off until merchants can save a card (P7 7d-2).
- **Paymob setup.** Charging a saved card needs Paymob to enable
  recurring payments on the platform's account and issue a **MOTO
  integration ID**. Ask the Paymob account manager first.
- **The platform's Paymob accounts.** Set one per currency the
  platform bills (worker): `BILLING_PAYMOB_<EGP|SAR|AED>_SECRET_KEY`,
  `…_API_KEY` and `…_MOTO_INTEGRATION_ID` (ADR-0052). Run the billing
  gateway's contract against Paymob's test mode first.
- **The platform's Paymob checkout.** Per currency (api and worker):
  `BILLING_PAYMOB_<CUR>_PUBLIC_KEY`, `…_CARD_INTEGRATION_ID` (3DS, with
  "save card" pre-checked: ask Paymob) and `…_HMAC_SECRET`. Set the
  account's callback URL to `https://<api host>/v1/webhooks/paymob_billing`.
  In test mode, confirm that the intention's answer carries
  `intention_order_id` (ADR-0053).
- **WhatsApp billing templates.** Have Meta approve the three merchant
  templates in `docs/modules/messaging.md` (ADR-0054). Until then, owners
  get billing notices by email only.
- **Turning collection on.** Once a region has both its account and its
  checkout, set `BILLING_COLLECTION_ENABLED=true`.


## 4. Email

Without SMTP, emails (sign-up, invitations, quota warnings) are not sent;
a masked notice is logged instead.

| Variable | Value |
|---|---|
| `SMTP_ADDR` | `host:587` of any SMTP provider |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | The provider's credentials (secret) |
| `MAIL_FROM` | e.g. `iftahonline <no-reply@iftahonline.com>`; set SPF, DKIM and DMARC for the domain |

## 5. Custom domains

Merchants point their domains at the platform (ADR-0037, U6e). On the api:

| Variable | Value |
|---|---|
| `CUSTOM_DOMAIN_TARGET` | The host merchants CNAME to, e.g. `connect.iftahonline.com`, pointed at the edge |
| `CUSTOM_DOMAIN_IPV4` | The edge's fixed public IPv4 addresses, for apex domains (required) |
| `CUSTOM_DOMAIN_IPV6` | The same in IPv6 (optional) |

## 6. Shoppers' search

The storefront needs its own Meilisearch key, limited to searching the
index (never the master key, never the worker's indexing key). Without
it, the storefront shows no search box.

| Variable (storefront) | Value |
|---|---|
| `MEILI_URL` | The Meilisearch address |
| `MEILI_SEARCH_KEY` | A key with the `search` action on `MEILI_INDEX` only (secret) |
| `MEILI_INDEX` | `products` (default) |

## 7. Edge cache purge

With `CLOUDFLARE_ZONE_ID` and `CLOUDFLARE_API_TOKEN` (worker; a token with
Zone › Cache Purge only), the worker purges changed pages. The storefront
may then use a longer `EDGE_CACHE_TTL`, e.g. `10m`. Without them, keep
`EDGE_CACHE_TTL` at `60s` or less.

## 8. Never in production

- `MESSAGING_FAKE_CHANNELS`, `SHIPPING_FAKE_CARRIER` and
  `PAYMENTS_FAKE_GATEWAY` are for development and tests: leave them unset.

## 9. Not verified against live accounts yet

These were built from the providers' documentation and pass their
contract tests against stand-ins. Each needs one real run, with a test
account, before merchants rely on it:

| What | How to check |
|---|---|
| WhatsApp Cloud API: send, statuses, button answers | Place a cash order on a demo store with your own phone; answer by button |
| Twilio SMS: send, status callback | Block WhatsApp on a test phone (or use a number without it) and place a cash order |
| Meta, TikTok, Snap Conversions APIs; GA4 Measurement Protocol | Connect each pixel with a test event code and a token; place an order; see the events in each provider's test tool with the browser's and the server's events counted once |
| Product feeds | Add the Google and Meta links to Merchant Center and Commerce Manager; check the first fetch's diagnostics |
| Paymob billing: saved-card checkout, the TOKEN and TRANSACTION callbacks, MOTO renewals | In Paymob's test mode, pay an invoice of a demo store on its page; renew it from the card on file (`BILLING_COLLECTION_ENABLED=true`); pay one invoice on two pages and see the second under the console's duplicate payments (ADR-0063) |
| Google AdSense in free stores (ADR-0062) | With the publisher's account: (1) `ads.txt` is read from the root domain, so serve `iftahonline.com/ads.txt` with the publisher's line (and, if AdSense asks, a `subdomain=` line per store host) rather than relying on each store host's `/ads.txt`; (2) open a free store's home with the browser's console open and add any origin the CSP blocks to `adSenseSources` (`internal/modules/storefront/internal/web/ads.go`); (3) check the units fill, non-personalized |

## 10. Known gaps

- GA4's server events carry our own client ID, not the one in the GA
  cookie, so GA4 may see a buyer's server-side purchase as another user.
- Google Ads has no server-side conversions yet (browser tag only).
- The message log's status tabs show no counts.
