# Deployment

One static binary (`platform`) serves every role. Ship it either as the
distroless image (`deploy/docker/Dockerfile`) or as a bare binary under
systemd (`deploy/systemd/`).

## systemd (Ubuntu LTS / Debian)

```sh
install -m 0755 dist/platform-linux-amd64 /usr/local/bin/iftahonline
install -d -m 0700 /etc/iftahonline
for role in api storefront worker migrate; do
  install -m 0600 deploy/systemd/env/$role.env.example /etc/iftahonline/$role.env   # then edit secrets
done
install -m 0644 deploy/systemd/*.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now iftahonline-api iftahonline-storefront iftahonline-worker
```

- **Migrations run first.** `iftahonline-migrate.service` is a oneshot that
  the role units `Require`. It connects as `app_owner`; the services connect
  as `app_user`, which is `NOBYPASSRLS` (ADR-0002). Concurrent migrators are
  serialised by a PostgreSQL advisory lock.
- **Hardening.** The units run with `DynamicUser`, `ProtectSystem=strict`,
  a syscall allow-list, no capabilities and private `/tmp`. Check a unit
  with `systemd-analyze security iftahonline-api`; CI keeps the exposure
  score at or below 2.0 (`make systemd-verify`).
- **Memory.** `MemoryMax` is the hard limit. `GOMEMLIMIT` is about 90% of
  it, so the Go GC tightens up before the kernel OOM-kills the process.
- **Shutdown.** On `systemctl stop` (SIGTERM):
  1. `/readyz` returns 503;
  2. the process waits `DRAIN_DELAY` so the load balancer can stop routing
     to it;
  3. in-flight requests get `SHUTDOWN_TIMEOUT` to finish;
  4. the process exits 0.

  The worker first finishes running jobs and cancels them after the
  timeout. `TimeoutStopSec` must stay above `DRAIN_DELAY + SHUTDOWN_TIMEOUT`.

## Database roles

Infrastructure creates the roles; migrations never do. In development,
`deploy/compose/initdb/01-roles.sql` does it. In production, create the
same three roles with secrets from the secret manager before the first
migration (`migrate up` refuses to run without them):

| Role | Used by | Attributes |
|---|---|---|
| `app_owner` | `migrate` | Owns the schema |
| `app_user` | `api`, `storefront`, `worker` | `NOSUPERUSER NOBYPASSRLS` |
| `app_admin` | The super-admin console (`ADMIN_DATABASE_URL`) and `platform admin create` | `NOSUPERUSER NOBYPASSRLS` |

```sql
CREATE ROLE app_admin LOGIN PASSWORD '…' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
```

## Secrets

The `api` and `worker` roles need two secrets:

- **`SECRETBOX_KEYS`**: `1:<base64 of 32 random bytes>`. To rotate, list
  the new key first.
- **`OTP_PEPPER`**: base64 of 32 or more random bytes.

Generate each with `openssl rand -base64 32`, and keep both in the secret
manager.

## Media (S3 and imgproxy)

The `api` and `worker` roles also need object storage and imgproxy
([ADR-0012](../docs/adr/0012-image-pipeline.md)):

- **`S3_ENDPOINT`**, **`S3_REGION`**, **`S3_BUCKET`**, **`S3_ACCESS_KEY`**,
  **`S3_SECRET_KEY`**: any S3-compatible store. Browsers upload straight
  to it, so set `S3_PUBLIC_ENDPOINT` when they reach it at another
  address, and allow CORS `POST` from the dashboard origin on the bucket.
- **imgproxy**: run it next to the storefront, behind the CDN. Give it
  the same `IMGPROXY_KEY` and `IMGPROXY_SALT` (hex, `openssl rand -hex
  32` each) as the api, read access to the bucket, and
  `IMGPROXY_ALLOWED_SOURCES=s3://<bucket>/`. Set `IMGPROXY_URL` to its
  public address.
- **`MEDIA_IMAGE_WORKERS`** (worker): concurrent image jobs. Each holds
  up to about 220 MB at the default 25 MP limit; size `GOMEMLIMIT`
  accordingly.

## Super-admin console

1. Set `ADMIN_DATABASE_URL` (the `app_admin` role) for the api role, and
   restart it.
2. Point `ADMIN_HOST` (default `admin.<PLATFORM_BASE_DOMAIN>`) at the api
   behind the edge.
3. Restrict that hostname at the edge to the operators' IP ranges.
4. Create the first admin. The password goes on stdin, and the TOTP
   enrolment is printed once:

   ```sh
   read -rs PW && printf '%s\n' "$PW" | ADMIN_DATABASE_URL=… SECRETBOX_KEYS=… \
     iftahonline admin create --email ops@iftahonline.com --name "Ops"
   ```

## Probes

| Path | Meaning |
|------|---------|
| `/livez` | The process is alive. Never checks dependencies. |
| `/readyz` | PostgreSQL and Redis (api/storefront) or PostgreSQL and the River schema (worker) answer within 1s; 503 while draining. |
| `/v1/meta/version` | Build version, commit, Go version, role. |

## Image

```sh
make image IMAGE=iftahonline:dev && make image-scan IMAGE=iftahonline:dev
docker run --rm -e DATABASE_URL=... -e REDIS_URL=... -p 8080:8080 iftahonline:dev api
```

The image runs as uid/gid 65532, contains no shell, and is rebuilt and
scanned with Trivy in CI. A HIGH or CRITICAL finding fails the build.
