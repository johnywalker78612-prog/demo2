# Hostinger deployment

Server: `179.198.202.47` (`srv2190047`), Ubuntu 24.04, Germany.

The Go executable embeds the website and runs as the `private-records` system user.
It uses the existing encrypted Neon PostgreSQL store. This is separate from the
TypeScript records API. Local and hosted record edits are not automatically synced.

- Binary: `/opt/private-records/server`
- Protected configuration: `/etc/private-records/app.env` (root-only)
- Service: `private-records.service`
- Backend binding: `localhost:10000` (not publicly exposed)
- Reverse proxy: Caddy, configuration `/etc/caddy/Caddyfile`
- Domain: `evisa-immigration.uk`; www redirects to the apex
- SSH identity on this Mac: `~/.ssh/visa_uk_hostinger`

`RECORDS_CLOUD` is intentionally unset: on a VPS the app binds to localhost,
while `DATABASE_URL` selects PostgreSQL storage independently. Secure cookies are
required and the public URL is HTTPS.

## Updates

Run from the repository root on a Mac/Linux computer with Go and SSH installed:

```sh
./scripts/deploy-hostinger.sh
```

The script runs Go tests, builds for Linux, uploads the executable, saves the old
executable as `server.previous`, restarts the service, and checks the homepage and
health endpoint. It restores the old executable if those checks fail. It preserves
the server environment and database. Active in-memory login sessions reset on restart.

To override the SSH identity or server:

```sh
HOSTINGER_SSH_KEY=/path/to/private-key HOSTINGER_TARGET=root@179.198.202.47 ./scripts/deploy-hostinger.sh
```

### GitHub updates

Use a checkout of `johnywalker78612-prog/visa_uk`, not the older `gov.uk` repository:

```sh
git clone https://github.com/johnywalker78612-prog/visa_uk.git
cd visa_uk
# Make your source changes, then inspect them:
git status --short
git diff
# Stage only intended source files; never environment files or records.
git add backend/internal/app backend/web scripts/deploy-hostinger.sh deploy/hostinger DEPLOY_HOSTINGER.md
git diff --cached --stat
git commit -m "Update application"
git push origin main
./scripts/deploy-hostinger.sh
```

For an existing clean checkout, use `git pull --ff-only origin main` before editing.
A GitHub push alone does not deploy the website. The script deploys the code in the
checkout where it is run, so commit and push that code first.

Service status and logs:

```sh
systemctl status private-records caddy
journalctl -u private-records -n 30 --no-pager
curl http://localhost:10000/healthz
```

## Domain and email

Set the GoDaddy apex A record to `179.198.202.47`, keep www as a CNAME to the
apex, and preserve mail/DKIM records. Remove conflicting parked apex A records.
DNS and valid HTTPS certificates for the apex and www were verified on 23 November 2026. Some clients may temporarily retain the old parked DNS address.

Email is optional. Configure Brevo or SMTP in the protected server environment
and restart the app to enable delivery. Never commit credentials. Until configured,
messages only enter the development outbox.

On initial deployment one local person record and its available photo were copied
via the authenticated API, retaining existing cloud history. The cloud person has
a newly assigned record ID; old verification sessions and links were not migrated.
