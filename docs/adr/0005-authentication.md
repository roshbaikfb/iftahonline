# ADR-0005: Authentication — opaque sessions, argon2id, phone OTP, TOTP

- Status: Accepted
- Date: 2026-10-18

## Context

Merchants sign in from the dashboard (browser) and the Flutter app. In
Egypt and the Gulf many prefer signing in with a phone number. Account
takeover is the most likely serious attack: credential stuffing, OTP
brute force, and SMS pumping fraud that bills us for messages to premium
numbers. Super-admins can see every store, so they need stronger
protection than merchants.

## Decision

1. **Sessions are opaque and server-side, not JWTs.**
   - **Token:** 32 random bytes, of which we store only the SHA-256 hash.
   - **Why:** revocation is immediate (log out everywhere, suspend a
     user, change a password), and no signing key can be stolen to mint
     sessions.
   - **Browser:**
     - The token travels in a `__Host-sid` cookie: `HttpOnly; Secure;
       SameSite=Lax; Path=/`, with no Domain attribute.
     - State-changing requests must also carry a matching `Origin`.
   - **Mobile app:** `Authorization: Bearer <token>`.
   - **Cache:** session lookups go to Redis, with PostgreSQL as the source
     of truth.
   - **Expiry:** sessions have an idle timeout and an absolute lifetime.
     The token rotates after login and after 2FA.
2. **Passwords use argon2id.**
   - **Parameters** come from config. The default is m = 19 MiB, t = 2,
     p = 1 (the OWASP minimum).
   - **Memory bound:** a semaphore limits concurrent hashes, so logins
     cannot exhaust memory under GOMEMLIMIT.
   - **Unknown accounts:** an unknown email still costs one dummy hash,
     so response time does not reveal whether an account exists.
3. **Sign-up uses email and password.** Phone OTP is a login method only,
   and only for an account whose phone number has been verified.
4. **Phone OTP:**
   - **Code:** 6 digits, stored only as HMAC(pepper, code). It expires
     after 5 minutes and allows 5 attempts.
   - **Resend:** allowed once every 60 s.
   - **Caps:** daily limits per number and per IP.
   - **Regions:** numbers only from the regions `platform/phone` accepts.
   - **Delivery:** a River job whose args carry only a challenge ID.
     - The plaintext code waits in Redis, sealed with `platform/secretbox`,
       for a few minutes.
     - The job deletes it after sending.
     - Neither the number nor the code ever reaches the job table or the
       logs.
   - **Provider:** behind an `OTPSender` interface. The provider (SMS or
     WhatsApp) is chosen later. Until then a development sender logs a
     masked notice.
5. **TOTP (RFC 6238):**
   - **Implementation:** the standard library, tested against the RFC
     vectors.
   - **Seed storage:** sealed with `secretbox`, with the user ID as
     associated data.
   - **Replay:** a used time step cannot be reused.
   - **Recovery codes:** 10, stored hashed.
   - **Disabling 2FA** requires re-authentication.
6. **Throttling** uses `platform/ratelimit` (Redis GCRA, failing closed
   with 503):
   - limits per IP and per identifier (email or phone);
   - failures are consumed, and successful logins are not;
   - responses are identical for unknown accounts and wrong passwords.
7. **New-device alerts.**
   - A long-lived device cookie identifies a browser or app install.
   - We store only its hash.
   - A login from an unknown device inserts an alert job in the same
     transaction.
   - Mail goes through a `Mailer` interface whose provider is chosen
     later.
8. **Super-admins are a separate population.**
   - **Storage:** their own table and their own sessions, in a
     `__Host-asid` cookie.
   - **Host:** served only on `admin.iftahonline.com`.
   - **2FA:** TOTP is mandatory at every login. There is no password-only
     path.
   - **Bootstrap:** the first admin is created with
     `platform admin bootstrap`, which prints the TOTP enrolment URI once.
   - **Sessions** are short-lived.
   - **Audit:** every admin action goes to an append-only admin audit log.

## Consequences

- **Positive:**
  - Stolen-token damage is bounded and revocable.
  - Brute force and SMS fraud are bounded per number, per IP and per day.
  - No PII or secrets reach jobs, logs or the job table.
- **Negative:**
  - Each authenticated request needs a session lookup. Redis absorbs it,
    and the database is hit only on a cache miss.
  - argon2id is deliberately expensive, so login capacity is sized
    together with the semaphore.
- **Deferred:** SMS/WhatsApp and email providers, passkeys/WebAuthn, and
  a CAPTCHA on repeated failures.
