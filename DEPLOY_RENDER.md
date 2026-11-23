# Deploy on Render Free

The Go server serves both the website and admin portal. Choose a **Web Service**,
not a Static Site. Keep the Free instance type. No paid disk or Render database
is needed. The existing Neon database holds an encrypted snapshot of the Go app's
records, photos, messages and sharing data in `private_records_cloud_state`.
The separate TypeScript records API and its tables are not changed or synchronized.

## Email delivery

Email is optional. Configure the existing Brevo or SMTP integration separately
using the settings documented in `backend/README.md`. With no configured provider,
messages remain in the development outbox and no email is sent.

## Prepare database (once, before the first web deployment)

If the database has already been prepared, skip this section. The initial setup
for this checkout was completed on 23 November 2026, including a byte-for-byte
verified import of its encrypted local file into the existing main Neon branch.

Use the existing encryption key; never generate a replacement for existing data.
Back up `backend/data/records.enc` and `backend/.env` separately.

From the repository root:

```sh
cd backend
go build -o bin/private-records ./cmd/server
set -a
. ../.env.neon.local
. ./.env
set +a
./bin/private-records -migrate
./bin/private-records -import-file data/records.enc
```

`-migrate` uses `DATABASE_URL_UNPOOLED`, creating an additive table only.
Import refuses to overwrite any nonempty cloud store. Omit import if starting
without a local data file. It does not remove or alter local records. After
cutover, use the hosted app for writes: the local file is not synchronized.

If your network blocks Postgres TCP, the equivalent HTTPS fallback, from the
repository root after `npm ci`, is:

```sh
node scripts/migrate-cloud-store.mjs .env.neon.local backend/data/records.enc
```

This validates the encrypted local file before importing and verifies the saved
bytes. It refuses to import if a cloud snapshot already exists, even if empty.
Omit the last argument to apply only the schema.

## Render dashboard

1. **New → Web Service → Git Provider → GitHub**. Authorize this repository:
   `johnywalker78612-prog/visa_uk`.
2. Choose branch **main**.
3. Use these settings:

   | Setting | Value |
   | --- | --- |
   | Name | `private-records-demo` (or an available name) |
   | Region | Singapore |
   | Language/runtime | Docker |
   | Root directory | Leave empty |
   | Dockerfile path | `./Dockerfile` |
   | Docker context | `.` |
   | Instance type | **Free** |
   | Health check path | `/healthz` |
   | Docker command | Leave empty |
   | Auto-deploy | Off initially |

4. Locally, run `python3 scripts/prepare-render-env.py` once. It creates
   `.env.render.local` with restricted file permissions and excludes SMTP/Brevo
   credentials. It does not print secret values. If the file already exists,
   edit it directly instead of regenerating it.
5. Use Render's **Add from .env** to import that file's contents. Configure
   email separately if needed. Do not commit this file.
6. Click **Deploy Web Service**. Wait for Live. The app automatically uses
   Render's supplied `RENDER_EXTERNAL_URL` for its initial public URL.
7. Check `/healthz`, `/`, and `/admin/login`. Username defaults to `admin`;
   password is the existing `RECORDS_ADMIN_PASSWORD` in `backend/.env`.
8. Create a fictional test record; verify photo saving and a restart/redeploy.
   Test email delivery to your own address through the admin outbox, then remove
   the temporary record. Inspect your email provider delivery status and your inbox.

Never add a payment method, paid disk, or paid instance for this setup. If a screen
requires billing, stop and check the selected plan instead of accepting a charge.

## Connect the domain after the temporary URL works

1. Open the web service's **Settings → Custom Domains → Add Custom Domain**.
2. Enter `evisa-immigration.uk`.
3. Copy Render's displayed DNS values into your current DNS provider. Preserve
   existing email records. Do not guess the service hostname or IP.
4. Verify the domain in Render and wait for its HTTPS certificate.
5. Set `RECORDS_PUBLIC_URL=https://evisa-immigration.uk` in Render and redeploy.
6. Check the root and www redirect, admin login, secure cookies and email links.

## Limits and recovery

- Free Render services sleep after inactivity; cold starts can take around a minute.
- Browser sessions are held in memory. Deploys/restarts require signing in again;
  pending browser verification flows must restart. Saved records persist in Neon.
- The encrypted-snapshot adapter is intended for a small demo, with a 32 MiB
  plaintext snapshot limit (including base64 photos). Every write saves the
  entire snapshot. Larger datasets need normalized tables and object storage.
- Mutations take a database row lock. Other instances refresh when the version
  changes; unavailable databases return 503 instead of serving cached records.
- `/healthz` is process liveness and deliberately does not wake Neon. Startup
  validates database access; test an authenticated route to verify live storage.
- Back up the cloud table with a Postgres dump and keep `RECORDS_DATA_KEY`
  separately. A cloud snapshot cannot be decrypted without the original key.
- Keep one running web service. The local encrypted file is a pre-cutover copy,
  not a cloud backup. Never overwrite newer cloud state by importing it again.

Validation: `go test -race ./...` and `go vet ./...` in `backend/`.
To include the isolated-database test, set `RECORDS_TEST_DATABASE_URL` to a direct
connection on a temporary Neon branch. The test creates and drops its own schema.

Official references:
[Render Docker](https://render.com/docs/docker),
[Free limits](https://render.com/docs/free),
[Custom domains](https://render.com/docs/custom-domains).
