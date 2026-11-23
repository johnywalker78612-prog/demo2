# Private Records and demo website

For cloud deployment, see [Render Free + Neon setup](DEPLOY_RENDER.md).

One Go server serves the twenty HTML demo pages, their local assets, the private-records REST API, and the admin portal. The static demonstration uses fictional details only and does not submit entered personal information or connect to government services. Admin records are managed separately.

## Run

Follow [backend setup](backend/README.md#run) for a new installation. If `backend/.env` already exists, reuse it:

```sh
cd backend
set -a
. ./.env
set +a
go run ./cmd/server
```

Open `http://localhost:8081/` for the homepage and `/admin/login` for the admin portal. Do not regenerate an existing encryption key. Use the Go server, rather than opening HTML files directly: the pages now use root-relative routes and asset URLs.

## Routes and files

| Original page | URL | HTML under `backend/web/pages/` | CSS under `backend/web/assets/css/` |
| --- | --- | --- | --- |
| 1 | `/` | `home.html` | `home.css` |
| 2 | `/visa` | `visa.html` | `visa.css` |
| 3 | `/evisa/view-evisa-get-share-code-prove-immigration-status` | `evisa-share-code.html` | `evisa-share-code.css` |
| 4 | `/gov/sign-in` | `demo/sign-in.html` | `sign-in.css` |
| 5 | `/gov/passport` | `demo/passport.html` | `passport.css` |
| 6 | `/gov/date-of-birth` | `demo/date-of-birth.html` | `date-of-birth.css` |
| 7 | `/gov/security-code-delivery` | `demo/security-code-delivery.html` | `security-code-delivery.css` |
| 8 | `/gov/security-code` | `demo/security-code.html` | `security-code.css` |
| 9 | `/gov/status` | `demo/status.html` | `status.css` |
| code_opt_1.html | `/gov/security-code-options` | `demo/security-code-options.html` | `security-code-options.css` |
| generate_share_Code3.html | `/gov/share-introduction` | `demo/share-introduction.html` | `share-introduction.css` |
| prove_status_4.html | `/gov/share-purpose` | `demo/share-purpose.html` | `share-purpose.css` |
| page5_det_photo.html | `/gov/share-preview` | `demo/share-preview.html` | `share-preview.css` |
| final.html | `/gov/share-result` | `demo/share-result.html` | `share-result.css` |

Page 8 navigates to `/gov/status` without verifying or transmitting a code. The status screen uses fictional values and a placeholder portrait. Its canonical HTML is `backend/web/pages/gov/status.html`, paired with `backend/web/assets/css/status.css`. The former import copies have been removed. `/extra/after_enter_code_1.html` redirects to `/gov/status`. Existing `/pages/pageN.html`, `/pageN.html`, and `/index.html` bookmarks redirect to the matching routes, without forwarding query values.

## Directory structure

```text
backend/
  cmd/server/main.go                 Server startup
  internal/app/
    website.go                      Public HTML route registry and asset handler
    website_test.go                 Route, isolation and asset tests
    server.go                       JSON REST API routes and middleware
    portal.go                       Admin handlers
    templates/portal.html           Admin template
    templates/portal.css            Admin styling
    ...                             Private-records application logic
  web/
    embed.go                        Bundles pages and assets into the Go binary
    pages/
      home.html
      visa.html
      evisa-share-code.html
      demo/                         Sign-in and navigation demo screens
    assets/
      css/                          Named page styles; unchanged style rules
        vendor/                     Original shared styles
        legacy/                     Retained older styles
        demo-notice.css             Separate visible demo notice
      js/                           Local controls, cookies and demo navigation
      icons/
      images/
      fonts/
      manifest.json
  previews/                         Existing admin screenshots
  openapi.json                       Private-records API specification
  data/                             Ignored, encrypted runtime data
  .env                              Ignored, local secrets
  go.mod
  Makefile
data/legacy/                        Retained original local data; never served
```

## Styles and navigation

All existing CSS rules, fonts, icons and images are retained. Page CSS files have descriptive names and keep their original directory relationship to vendor imports. Only HTML references change to `/assets/...`. Shared JavaScript and manifest icons retain their directory relationships. A separate notice identifies every public screen as a non-government demo.

The Go server returns `text/html` for website routes and JSON for `/api/v1/...`. Demo form buttons navigate locally without serializing, saving or forwarding values. Forms remain non-submitting when JavaScript is unavailable. External navigation and network APIs remain blocked. Cookie preferences still apply across the site. The existing resend adapter sends no email.

## Add future routes

1. Add the HTML file under `backend/web/pages/`.
2. Add its `{Path, File}` entry to `PageRoutes` in `backend/internal/app/website.go`.
3. Use root-relative links and assets; add a named CSS file if needed.
4. Keep the visible demo notice and preview-only behavior for demonstration screens.
5. Run `go test -race ./...` from `backend/`, rebuild and restart.

Pages and assets are embedded, so deployment requires the compiled binary and runtime secrets/data, not a separately mounted source directory. Rebuild after HTML/CSS edits. Only allowlisted page routes and files below `/assets/` are served; directory listings and private backend files are not exposed.

All six former HTML files from `extra/` are now maintained only in `backend/web/pages/gov/`; duplicate HTML and CSS import files have been removed. Their `/extra/<filename>.html` URLs redirect to the routes above. Each matching stylesheet is copied without changing its CSS rules; font and crest URLs point to local assets. The supplied icon font is bundled locally. The share flow displays `DEMO-NOT-VALID`; print, PDF, email and unsupported external links remain unavailable. No official share code is generated.

The status/share stylesheets were checked against the supplied HAR capture: their rules match the captured `app-cac10649.css` (formatting and local URLs aside). The two captured WOFF2 fonts are bundled under `assets/fonts/status/` for these five pages. The HAR itself and its session data are not bundled. The security-code options CSS was not present in that capture and is maintained in `backend/web/assets/css/security-code-options.css`.

## New supplied screens

The six former HTML files from `new_pages/` are maintained only in `backend/web/pages/gov/new-*.html` and are served under `/gov/new/verification`, `/gov/new/status`, `/gov/new/share-introduction`, `/gov/new/share-purpose`, `/gov/new/share-preview`, and `/gov/new/share-result`. Original `/new_pages/<filename>.html` URLs redirect to these previews. Each uses its matching supplied stylesheet, copied as `assets/css/new-<screen>.css` with only local asset URL rewrites. Status/share fonts and captured icons come from the supplied HAR; verification fonts, icons and footer crest come from its public static asset paths. Demo notices, fictional records and invalid codes remain explicit. Open through the Go server, not a file URL.

## Canonical pages and backend connections

`backend/web/pages/` and `backend/web/assets/` are the maintained public-page sources. The duplicate `extra/` and `new_pages/` import directories have been removed after verifying their matching backend copies. Old import URLs remain redirects. `backend/previews/` contains an independent admin screenshot gallery, not duplicate live pages, and is retained.

Private Records uses server-rendered Go forms and the same service functions as the REST API; it does not issue redundant HTTP calls to its own API.

| Private Records screen | Backend connection |
| --- | --- |
| `/admin/login`, `/admin/logout` | Session authentication and logout |
| `/admin`, `/admin/people` | Reports, search, sorting and paginated stored records |
| `/admin/people/new`, `/admin/people/{id}` | Create/edit record and atomic photo attachment; crop editor |
| `/admin/people/{id}/delete`, `/admin/people/{id}/photo/delete` | Delete record or photo |
| Profile email-verification control | Queue branded verification message |
| `/admin/outbox` | Preview, explicitly send via configured SMTP, or delete messages |
| `/verify-code/{challenge}`, `/verify-email/{token}` | One-time email-ownership confirmation |
| Profile internal-sharing controls | Create, revoke, delete and queue sharing emails |
| `/shared/{token}` | Limited stored record, with expiry/revocation checks |
| `/shared/{token}/pdf` | Private Records PDF export; printable shared page |
| `/admin/audit` | Stored audit events |

The government-style `/gov/...` pages remain static previews. They are not connected to personal records, real email authentication, or generated proof-of-status documents. SMTP credentials are still required before actual email delivery can be tested. See `backend/README.md` for configuration and REST endpoints.
# visa_uk
# demo2
