# Private Records admin API on Neon

Deployed endpoint:
`https://br-ancient-mouse-b3wm2etk-records.compute.c-4.ap-southeast-1.aws.neon.tech/`

This TypeScript/Hono service migrates the admin record CRUD, internal ID + DOB lookup, and photo operations to Neon Postgres and private Object Storage. It is an independent, server-to-server admin API. Existing HTML/CSS is unchanged. The Go admin UI still uses its original local encrypted store; its existing records are not copied automatically. This is not a migration of the Go portal login, reporting, email/SMS, or sharing/PDF services. It does not serve the website or authenticate users by passport/DOB.

## Access

Every `/api/*` request requires `Authorization: Bearer <RECORDS_API_TOKEN>`. The generated deployment secret and URL are stored in the ignored, mode-0600 `.env.neon.local`. Never place the secret in HTML, browser JavaScript, a query string, logs, or commits. Use the API from an authenticated application server. CORS is deliberately not enabled. `/healthz` is public and contains no record data. `/api/v1/health` checks the database with authentication.

## Routes

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/api/v1/people?page=1&page_size=25` | Paginated records, newest first; max page size 100 |
| POST | `/api/v1/people` | Create a record; `first_name` required |
| GET | `/api/v1/people/{id}` | Retrieve a record |
| PATCH | `/api/v1/people/{id}` | Update supplied fields; concurrent updates are row-locked |
| DELETE | `/api/v1/people/{id}` | Delete record and queue associated photo cleanup |
| POST | `/api/v1/people/lookup` | JSON `{"id":"person_…","date_of_birth":"1990-03-09"}`; both must match |
| PUT | `/api/v1/people/{id}/photo` | Raw PNG/JPEG body; matching content type; max 5 MiB |
| GET | `/api/v1/people/{id}/photo` | Authenticated image response, never a public bucket URL |
| DELETE | `/api/v1/people/{id}/photo` | Remove the photo association and queue storage cleanup |

Fields: `first_name`, `last_name`, `email`, `phone_number`, `date_of_birth`, `nationality`, `passport_number`, `recorded_status`, `accompanied_by`, `valid_from`, `valid_until`, `entries_allowed`, `national_insurance_number`. Server-owned IDs, timestamps, `has_photo`, and `unverified` cannot be supplied. Dates use `YYYY-MM-DD`. All records are unverified. Matching DOB is a filter, not proof of identity.

## Build, test, deploy

- `npm run build` — type-check and build the TypeScript service.
- `npm run test:api` — local authentication and validation tests.
- `npm run test:live` — exercise the deployment using a temporary fictional record and image; removes the test data afterward.
- `npm run build:go` — build the retained Go application.
- `neon config plan --env .env.neon.local` — inspect proposed infrastructure changes.
- `neon deploy --env .env.neon.local --no-env-pull` — deploy without changing existing environment files.

`neon.ts` preserves the private `assets` bucket and adds the `records` function. Neon injects Postgres and S3 settings. `schema.ts` contains the additive version-1 migration, applied transactionally under an advisory lock on the first authenticated database operation. No existing local data is imported.

Photo replacement/deletion records obsolete keys in a durable cleanup queue; successful photo changes and deletions retry up to 10 queued removals. A failed upload-to-database transition can leave an unreferenced object; periodic reconciliation is needed for production operation. Photos receive file-signature checks but are not cropped or re-encoded by this API.

Hosting the frontend and connecting the Go admin UI to this service remain separate work. Keep the deployed admin token server-side when doing so.
