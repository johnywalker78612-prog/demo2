# Private Records — standalone Go API and admin portal

A separately branded, local records-management service. Every record is **unverified**. Internal sharing links do not constitute an immigration decision, an official share code, or proof of immigration status. The Go server also serves clearly labelled static demo pages at the routes listed in the [project README](../README.md). These pages do not submit personal details, accept government security codes, or connect to the private-records API or external services. No real people's details or photos are seeded.

## Run

Requires Go 1.26 or later. PDF generation uses `github.com/go-pdf/fpdf` and the Go fonts from `golang.org/x/image`; dependencies are pinned in `go.mod` and `go.sum`.

```sh
cd backend
umask 077
go run ./cmd/server -init > .env
set -a
. ./.env
set +a
go run ./cmd/server
```

Open `http://localhost:8081/admin`. Sign in with username `admin` (or your configured username/email) and `RECORDS_ADMIN_PASSWORD` from your local `.env` file. The REST API uses `RECORDS_API_TOKEN` as a bearer token. These are separate secrets. Do not regenerate or lose `RECORDS_DATA_KEY` after creating records: the existing encrypted database cannot be read without it. Keep `.env` private and back it up separately from the database.

Run `-init` **once** for a new installation. Never overwrite an existing `.env` with fresh keys unless intentionally starting a new database.

```sh
go test -race ./...
go vet ./...
go build -o bin/private-records ./cmd/server
```

## Configuration

| Environment variable | Meaning |
| --- | --- |
| `RECORDS_ADMIN_USERNAME` | Login username; defaults to `admin`. |
| `RECORDS_ADMIN_EMAIL` | Optional alternative email login for the same administrator. |
| `RECORDS_ADMIN_PASSWORD` | Admin portal password, minimum 12 characters; `-init` generates a random secret. |
| `RECORDS_API_TOKEN` | REST API bearer token, minimum 32 characters. |
| `RECORDS_DATA_KEY` | Base64-encoded 32-byte AES key. |
| `RECORDS_DATA_FILE` | Encrypted database path; default `data/records.enc`, relative to the working directory. |
| `RECORDS_ADDR` | Local listening address; default `localhost:8081`. This edition only binds to `localhost`. |
| `RECORDS_SECURE_COOKIES` | Set `true` only when using HTTPS; local HTTP development defaults to `false`. |

## REST API

Import [`openapi.json`](openapi.json) into an API client for the route and request schemas.

Paths below are relative to `http://localhost:8081`. All routes except health, shared-link resolution and email confirmation require `Authorization: Bearer <RECORDS_API_TOKEN>`. Portal session cookies cannot authenticate API requests. JSON writes require `Content-Type: application/json`. Photo uploads use raw JPEG/PNG bytes.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Health status. |
| POST | `/api/v1/people` | Create an unverified person record. |
| GET | `/api/v1/people?q=example` | Search name, email, passport or phone; filter, sort and paginate, admin only. |
| GET | `/api/v1/people/{id}` | Read a person. |
| PATCH | `/api/v1/people/{id}` | Update supplied fields only. |
| DELETE | `/api/v1/people/{id}` | Delete person, photo, links, pending verifications and outbox messages; retain non-PII audit events. |
| PUT | `/api/v1/people/{id}/photo` | Add/replace a JPEG/PNG photo, max 5 MB and 4000×4000 pixels. |
| GET | `/api/v1/people/{id}/photo` | Read photo, admin only. |
| DELETE | `/api/v1/people/{id}/photo` | Remove photo. |
| POST | `/api/v1/people/{id}/shares` | Create an internal link; `{"expires_in_hours":24}`. Default 24 hours, max 168. |
| GET | `/api/v1/people/{id}/shares` | List link metadata, without plaintext tokens. |
| GET | `/api/v1/shares/{id}` | Read link metadata. |
| POST | `/api/v1/shares/{id}/revoke` | Revoke immediately; keep metadata. |
| DELETE | `/api/v1/shares/{id}` | Delete and invalidate immediately. |
| POST | `/api/v1/shares/{id}/messages` | Create a development outbox message for a valid link: `{"token":"internal_…","recipient":"recipient@example.invalid"}`. Does not send email. |
| GET | `/api/v1/shared/{token}` | Resolve an active internal link to a limited, explicitly unverified record. |
| POST | `/api/v1/people/{id}/email-verifications` | Generate a 15-minute, single-use verification link in the development outbox. A new request invalidates prior verification links. |
| POST | `/api/v1/email-verifications/confirm` | Confirm email ownership with `{"token":"verify_…"}`. Does not log in a person or verify their immigration details. |
| GET | `/api/v1/outbox` | Admin-only development messages, including test verification links. |
| DELETE | `/api/v1/outbox/{id}` | Remove a development message. |
| GET | `/api/v1/audit` | Mutation audit events: action, resource ID and UTC timestamp; no names, IPs or field values. |

Creation returns `201`, development-message requests return `202`, and successful deletions/revocations return `204`. Errors are JSON: `{"error":"…"}`. Typical errors are `400` malformed JSON, `401` missing/incorrect API credentials, `403` invalid portal CSRF token, `404` missing/inactive link or record, `413` oversized upload, `415` incorrect JSON content type, and `422` invalid field values.

### Person fields

Only `first_name` is required; `last_name` may be empty for people with a single name. All writable values are strings, up to 250 bytes. Dates use `YYYY-MM-DD`; invalid calendar dates, future birth dates, and inverted validity ranges are rejected.

```json
{
  "first_name": "Example",
  "last_name": "Record",
  "email": "example@example.invalid",
  "date_of_birth": "1990-03-09",
  "nationality": "Example nationality",
  "passport_number": "DEMO-ONLY",
  "recorded_status": "User supplied example",
  "accompanied_by": "",
  "valid_from": "2026-01-01",
  "valid_until": "2026-12-31",
  "entries_allowed": "Multiple",
  "national_insurance_number": ""
}
```

`id`, `unverified`, `has_photo`, `email_verified_at`, `created_at` and `updated_at` are read-only. A client's input cannot mark a record verified. Updating an email clears its verified state and invalidates pending verification links. Passport numbers and dates of birth are record fields, **not authentication credentials**; there is no public lookup by either.

### Existing records

Older encrypted records containing `name` remain readable: the complete legacy name is kept in `first_name` with an empty `last_name`, rather than guessing where to split it. Edit the fields in the admin portal when the correct split is known. Subsequent database writes use the new fields. API writes now accept `first_name` and `last_name`; the old `name` input is rejected. Name search matches the combined display name, and PATCH updates either field independently.

### Examples

After sourcing `.env` as above:

```sh
curl -sS http://localhost:8081/api/v1/people \
  -H "Authorization: Bearer $RECORDS_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"first_name":"Example","last_name":"Record","email":"example@example.invalid"}'
```

Use the returned ID below:

```sh
curl -sS -X PATCH http://localhost:8081/api/v1/people/PERSON_ID \
  -H "Authorization: Bearer $RECORDS_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"recorded_status":"User supplied example"}'

curl -sS -X PUT http://localhost:8081/api/v1/people/PERSON_ID/photo \
  -H "Authorization: Bearer $RECORDS_API_TOKEN" \
  -H 'Content-Type: image/png' --data-binary @example-photo.png

curl -sS http://localhost:8081/api/v1/people/PERSON_ID/shares \
  -H "Authorization: Bearer $RECORDS_API_TOKEN" \
  -H 'Content-Type: application/json' -d '{"expires_in_hours":24}'
```

Share creation returns `share`, `token`, `path` and `api_path`. Resolve `api_path` to get JSON or open `path` for an HTML view. The token appears only on creation; subsequent lists return metadata. Save the creation response if you need the token for the sharing-message endpoint. Generate another link if lost. Creating another link does not revoke earlier links; revoke them explicitly.

Shared views expose only first name, last name, nationality, recorded status, validity dates, link expiry and the unverified disclaimer. They omit passport number, date of birth, email, National Insurance number, accompanied-by data and photo. Anyone holding a live internal link can read that limited view, so treat the link as a secret.

## Admin portal

The Go server renders the portal using `html/template`; JavaScript enhances photo cropping and printing. It includes username/email login and logout, dashboard reports, name/email/passport/phone search, sorting and filters, record creation/editing/deletion, image upload/removal, link generation/revocation/deletion, email verification, outbox preview/sending/deletion and an audit view. Delete-person operations require a confirmation screen. All authenticated form mutations require session-bound CSRF tokens; sessions expire after 12 hours and are stored only in memory. Five failed admin logins trigger a one-minute global cooldown.

For email testing, generate a message from the person's record, open the admin development outbox, and copy its `/verify-email/…` path into the same local server address. Opening the link does not consume it; pressing **Confirm my email** consumes it. This uses only this application's own verification tokens, not government security codes. Outbox responses explicitly return `email_sent: false`.

## Storage and limits

The single encrypted JSON database includes photos, outbox contents and audit events. AES-256-GCM protects it at rest, with fresh random nonces and an external key. Writes use an in-process lock and atomic file replacement; failed persistence leaves the previous in-memory state unchanged. New files are mode `0600` and new data directories `0700`. Photo uploads are decoded and re-encoded to remove embedded metadata. Link and verification tokens use cryptographic randomness and are indexed by hashes; development outbox messages necessarily contain their test links inside the encrypted database.

This is a **local, single-process prototype**, not a hosted production deployment. Do not run two processes against the same data file. Records and audit events are held in memory and the full database is rewritten on changes, so this design targets small local datasets. For real multi-user hosting, add a transactional database, production identity management/MFA, TLS, key management, backup/retention controls, pagination and per-user authorization. Deletion removes active data, not copies in independently retained backups. SMTP delivery is optional and only runs after an explicit authenticated Send action. Government validation and private-record integration into the static demo pages remain unimplemented.

## Admin screens and reports

The portal is server-rendered Go HTML/CSS. Local JavaScript enables photo cropping and printing; no external assets are loaded.

- `/admin/login`: username or configured email plus password, with inline errors and rate limiting.
- `/admin`: dashboard, current record count, additions this month and over 3, 6 and 12 calendar months, monthly chart and accessible count table.
- `/admin/people`: searchable directory with nationality and email-ownership filters, ascending/descending sorting and 25 records per page.
- `/admin/people/new`: add a person; opening a person allows editing, photo management, internal sharing and deletion with confirmation.
- `/admin/audit`: activity log. `/admin/outbox`: locally generated development messages.
- Sign out invalidates the server session; a protected page then redirects to login.

Phone numbers are optional (`phone_number`); accepted formatting includes `+`, spaces, parentheses, dots and dashes, with 7–15 digits. Existing encrypted records remain compatible. Public internal-share responses never expose phone numbers.

Reports use **Asia/Kolkata** calendar months, including the current partial month, through the current time. The 12-month report is a trailing 12-calendar-month view, not a previous calendar-year report. Creation audit events are counted once per record, including people later deleted; older records without a creation event use `created_at`. Updates do not count as additions. The current total counts only records that still exist. The monthly breakdown also counts deletion events. This edition holds all records in memory and does not provide multi-admin accounts.

`GET /api/v1/reports?months=1|3|6|12` returns the same metrics (bearer authentication required; defaults to 12). The people API accepts `q`, `field=all|name|email|passport|phone`, `nationality`, `verification=all|verified|pending`, `sort=created_at|updated_at|name|email|nationality|phone_number|passport_number`, `order=asc|desc`, and `page`. Default ordering is newest first; the result includes `items`, `total`, `page`, and `page_size` (25). Phone searches ignore formatting. Sorting and pagination preserve all filters.

Email-ownership confirmation is separate from verification of record contents: all records remain unverified. No email is sent by default; configure SMTP and explicitly send a queued message to enable delivery.

Screenshots of all primary flows using fictional records are in [`previews/index.html`](previews/index.html). They are static previews, not a live login form.

## Public demo website

`/` now renders the demo homepage; the admin portal remains `/admin`. The twenty public routes and asset mapping are documented in the [project README](../README.md). HTML/assets are embedded from `web/`, so rebuild the server after editing them. Website responses use their own local-asset CSP with network APIs and form submission disabled. Admin/API policies and authentication remain separate. Unknown routes return 404. `/gov/status` is a static fictional preview, not a result of identity or security-code verification.

### Admin photo attachment and cropping

On Add person or Edit details, choose a JPEG/PNG and adjust zoom, horizontal position and vertical position in the 35:45 portrait preview. Saving stores a 700 × 900 JPEG together with the record in one transaction. Editing without choosing a new photo preserves the existing photo. Invalid uploads do not create a partial record or replace the previous photo. Original uploads must be at most 5 MB and 4000 × 4000 pixels. Cropping runs locally in the browser; without JavaScript, the original selected image is uploaded instead. This crop does not certify compliance with official passport-photo requirements. The authenticated photo REST endpoints remain available for upload, retrieval and deletion.

## Private Records emails and PDF export

All templates identify **Private Records**, an independent service. No government sender, logo, or claim of official status is used. Verification messages show the person's name and the last four passport characters only (omitted for passport values of four characters or fewer). Sharing emails omit passport details.

1. In a person's admin profile, click **Create verification message**.
2. Open **Email outbox**, then **Preview Private Records email**. Messages are queued, not automatically sent.
3. With SMTP configured, choose **Send email**. Without SMTP, test the code/verification URL from the plain-text message locally.
4. The recipient enters the six-digit code at the Private Records verification URL. Codes expire after 10 minutes, are single-use, and are invalidated after five wrong attempts. Requesting a replacement invalidates the previous code. Email confirmation does not verify identity or immigration status.
5. Create an internal sharing link in the profile. Use **Create sharing email** to queue a message to a chosen recipient. The shared page offers **Download private-record PDF** and **Print private record**.

The PDF is generated by Go and visibly says **Unverified private record - not proof of immigration status**. It includes only the fields available on the limited shared view, the internal sharing reference, access link, expiry, and export time. It excludes passport number, date of birth, contact information and photo. Expired/revoked/deleted links cannot download a new PDF. A downloaded file cannot itself be revoked and may become outdated.

### SMTP configuration

Add these settings to your existing local `.env`; do not regenerate database keys. Keep provider credentials in the local environment, not source control.

```sh
RECORDS_PUBLIC_URL=http://localhost:8081
RECORDS_SMTP_HOST=smtp.your-provider.example
RECORDS_SMTP_PORT=587
RECORDS_SMTP_USERNAME=your-provider-username
RECORDS_SMTP_PASSWORD=your-provider-app-password
RECORDS_SMTP_FROM=your-verified-sender@example.com
RECORDS_SMTP_IMPLICIT_TLS=false
```

Use a sender address authorized by your mail provider. STARTTLS is required on the normal SMTP connection; use `RECORDS_SMTP_IMPLICIT_TLS=true` with the provider's implicit-TLS port (commonly 465). Certificate validation remains enabled. Set `RECORDS_PUBLIC_URL` to your own reachable HTTPS origin for recipients outside this computer; localhost links only work on the machine running the server. Restart after changing configuration. Leaving `RECORDS_SMTP_HOST` empty keeps development outbox mode.

Brevo transactional email is also supported. Set `BREVO_API_KEY`, `FROM_EMAIL`, and `FROM_NAME`; Brevo is preferred when `BREVO_API_KEY` is present and uses `POST https://api.brevo.com/v3/smtp/email`. The sender must be verified in Brevo. Do not commit the API key.

HTML and plain-text MIME versions use the same record data. Delivery errors leave the message unsent. A successful SMTP acceptance records `sent_at`; this is not proof of inbox delivery. Already-sent, expired, superseded-verification and revoked-sharing messages are refused. Like other SMTP senders, a crash after provider acceptance but before saving the timestamp may require checking the provider before retrying.

### API additions

- `POST /api/v1/people/{id}/email-verifications`: queues the branded message; returns `outbox_message_id`, `challenge_id` and `email_sent:false`.
- `POST /api/v1/email-verifications/confirm`: accepts `{"challenge_id":"...","code":"123456"}` or the existing one-time `{"token":"verify_..."}` format.
- `POST /api/v1/shares/{id}/messages`: queues a branded sharing email using the existing token/recipient request.
- `POST /api/v1/outbox/{id}/send`: authenticated explicit SMTP send; returns `email_sent:true` only after SMTP acceptance and recording success.
- `GET /api/v1/shared/{token}/pdf` and `GET /shared/{token}/pdf`: return the limited private-record PDF while the bearer link remains valid.

Outbox preview and Send actions require admin login and Send requires CSRF protection. Email creation and sending APIs require the admin bearer token. No emails are sent by the automated tests.

### Email logo image

`internal/app/templates/email.html` is the HTML email template. Its top image uses `<img src="{{.LogoURL}}" alt="Private Records logo">`. Set `RECORDS_EMAIL_LOGO_URL=https://your-own-domain.example/logo.png` in the runtime environment and restart. Use your own publicly reachable HTTPS PNG/JPEG; images may be blocked by a recipient's mail client. The Private Records text heading stays visible even when images are blocked. With no logo URL configured, the image is omitted. This affects newly created messages; existing queued messages retain their original HTML. The code, expiry, link and sender wording remain specific to Private Records.

### Admin lookup by record ID and date of birth

`POST /api/v1/people/lookup` requires the same administrator Bearer token as the other people APIs. Send JSON such as `{"id":"person_example","date_of_birth":"1990-03-09"}`. The ID is the internal `id` returned when creating a record, not a passport number. Both fields must match; an unknown ID or mismatched birth date returns the same 404 response. This is an administrator-only search, not a sign-in or identity-verification endpoint.

Existing operations remain `POST /api/v1/people`, `GET /api/v1/people/{id}`, `PATCH /api/v1/people/{id}`, and `DELETE /api/v1/people/{id}`. Never embed the admin Bearer token in public page JavaScript. These endpoints currently use the existing encrypted local store; Neon migration is not implemented.

### Codes for ID and date-of-birth sign-in

The demo has no configured OTP delivery provider. Delivery requests return
HTTP 503. The send and verification functions generate random six-digit codes
and enforce a ten-minute expiry, five attempts, and single use.
