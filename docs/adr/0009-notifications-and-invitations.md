# ADR-0009: Delivering codes and emails without a provider; invitation tokens

- Status: Accepted
- Date: 2026-10-18

## Context

P1 needs phone OTP, invitation emails and new-device alerts. The SMS,
WhatsApp and email providers were deliberately deferred (decision of
2026-10-18). Developers still need to receive codes and links locally.
The security rules forbid logging OTPs, tokens or phone numbers, and side
effects must run as jobs.

## Decision

1. **Mail goes over SMTP.**
   - Every provider (SES, Postmark and others) and Mailpit speak SMTP.
   - STARTTLS is used when offered, and it is mandatory when credentials
     are set.
   - With no `SMTP_ADDR`, a log-only mailer records the masked recipient
     and the subject. It never logs the body, which may contain links with
     secrets.
2. **Phone codes:**
   - **Outside production**, codes are emailed to `<digits>@sms.invalid`.
     With the compose stack they appear in Mailpit, and never in logs.
   - **In production**, phone OTP returns **503** until a provider
     implements `notify.OTPSender`. Nothing is sent by a fallback channel.
3. **Code delivery:**
   - The code (or link) and the phone number wait in Redis, sealed with
     `secretbox`, with a TTL.
   - The job args carry only an ID, so the job table never holds a secret
     or a phone number.
   - The worker deletes the sealed value after a successful send. An
     expired value ends the job without a retry.
4. **Invitation tokens** have the form `<store ULID>.<256-bit secret>`.
   - Accepting reads the store from the token and opens that tenant
     directly, so no cross-tenant lookup is needed. Only the SHA-256 of
     the token is stored.
   - The link puts the token in the URL **fragment**
     (`/invite#<token>`). Browsers never send fragments to servers, so the
     token stays out of access logs and `Referer` headers.
5. **Devices:**
   - A browser gets a random `__Host-did` cookie on routes that can open a
     session. Apps send `X-Device-ID`.
   - We store only its hash.
   - A user's first device does not alert. A later unknown device enqueues
     an alert in the session's transaction.

## Consequences

- **Positive:**
  - Any SMTP provider can be adopted with configuration only.
  - Codes are testable end to end today.
  - No secrets reach logs or the job table.
- **Negative:**
  - Phone sign-in is unavailable in production until a provider is wired.
    The sign-up and login flows do not depend on it.
