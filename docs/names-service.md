# Deploying the name service

`soundstorm-names` gives every install a real name and, through it, a real
certificate. Why it exists and what it refuses to do is in CLAUDE.md under
"Real certificates, and the one service SoundStorm runs". This page is the
setup, which happens once.

It needs three things: the domain at Porkbun, somewhere to run one small
container (Railway here), and about fifteen minutes.

## 1. Porkbun

1. **Domain Management**, find `soundstorm.dev`, open **Details**, and switch
   on **API Access**. Leave it off for every other domain: Porkbun keys are
   account-wide, and this switch is the only thing limiting one to a single
   domain.
2. **Account → API Access → Create API Key.** Porkbun shows the secret once.
   Put both halves straight into Railway (next step), not into a file, a chat
   or a commit.

Nothing else changes at Porkbun. The domain keeps its nameservers, and any
website or mail on it is untouched: installs live under `home.soundstorm.dev`.

## 2. Railway

1. **New service → GitHub repo** → this repository. Its first build will use
   the root Dockerfile, which is SoundStorm itself; the variable below fixes
   that. Leave **Root Directory** empty: the build needs the whole repository.
2. **Variables:**

   | Variable | Value |
   | --- | --- |
   | `RAILWAY_DOCKERFILE_PATH` | `cmd/soundstorm-names/Dockerfile` - which image to build. A variable rather than Railway's config-file setting, which was deprecated between writing this and first using it; a variable is visible beside the others and does not depend on a Railway file format. |
   | `NAMES_SECRET` | 48 random bytes: `openssl rand -base64 48`. Keep a copy somewhere safe - changing it makes every install register again. |
   | `PORKBUN_API_KEY` | the `pk1_...` half |
   | `PORKBUN_SECRET_API_KEY` | the `sk1_...` half |
   | `NAMES_CLIENT_IP_HEADER` | `X-Real-IP` - the header Railway's proxy puts the caller's address in, used for rate limiting. Measured, not assumed: see below. |

3. **Settings → Deploy → Healthcheck path:** `/healthz`, if the setting is
   there. Optional; it lets Railway tell a working deploy from a broken one.
4. **Settings → Networking → Custom domain:** `names.soundstorm.dev`. Railway
   shows a CNAME target; add that record at Porkbun (**DNS** for
   `soundstorm.dev`, type CNAME, host `names`). Railway issues the
   certificate for it by itself.

Check it: `https://names.soundstorm.dev/healthz` answers `{"status":"ok"}`,
and `https://names.soundstorm.dev/v1/whoami` answers with *your* public
address as `clientIP`. If it shows some other address, every install is
sharing one registration limit - see the next section.

### Why X-Real-IP, and not X-Forwarded-For

Measured on 2026-09-23 through `/v1/whoami`, because Railway's own guidance
contradicted itself (its staff recommend the *first* X-Forwarded-For entry and
call X-Real-IP broken behind their CDN):

- `X-Real-IP` carried the real client address, and a forged `X-Real-IP` sent
  by the client was overwritten. Correct and unspoofable.
- `X-Forwarded-For` arrived as `<client>, <Railway edge>`, with a forged value
  stripped. The service reads the *last* entry of a list - right for a proxy
  that appends - and on Railway that is Railway's own edge server, so setting
  this header would put every install in the world behind one limit of ten
  registrations an hour.

If Railway ever changes this (it has said X-Real-IP will be "fixed" for its
CDN path), `/v1/whoami` is how to notice.

## 3. One install, against staging

Let's Encrypt's staging environment has generous limits and issues
certificates no browser trusts, which is exactly right for a first run. In
the `.env` beside `docker-compose.yml`:

```sh
SOUNDSTORM_TLS=auto
SOUNDSTORM_ACME_DIRECTORY=https://acme-staging-v02.api.letsencrypt.org/directory
```

`SOUNDSTORM_TLS_HOSTS` must hold the machine's LAN address, which the
installer already writes. Then `docker compose up -d` and watch:

```sh
docker compose logs -f soundstorm | grep -i -E 'name|certificate'
```

Expect "registered a name", "asking for a certificate", and within a couple
of minutes "real certificate installed". The page will not move to the new
address, because Chrome does not trust staging - that is the next step's job.

If it fails, the log line says why. The common ones:

- **"Domain is not opted in to API access"** - step 1.1.
- **"not being served yet"** - Porkbun took longer than four minutes to
  publish the challenge. It retries by itself in five minutes.
- **a `rateLimited` error** - staging's limits are generous, so something is
  looping. Stop it and look.

## 4. Production

Remove the `SOUNDSTORM_ACME_DIRECTORY` line and restart. The certificate is
then real: open `http://localhost:8099` and the page should move itself to
`https://<id>.home.soundstorm.dev:8099` with no warning. If it stays on http,
the router is refusing to resolve a public name that points at a home address
(DNS rebinding protection); that is a fallback working, not a failure.

## Limits worth knowing before there are many installs

- **Let's Encrypt allows about fifty new certificates a week per registered
  domain,** and every install is under `soundstorm.dev`. Renewals do not
  count. The fix is the Public Suffix List (publicsuffix.org), which makes each
  install its own domain to Let's Encrypt; apply well before it matters, as
  review takes weeks.
- **Porkbun allows 2,500 DNS records per domain** (`ZONE_RECORD_LIMIT` in its
  OpenAPI spec, `https://porkbun.com/api/json/v3/spec`, read 2026-09-23). Every
  install keeps one record while it is in use, so this is the real ceiling -
  roughly 2,400 installs running at once - and it is lower than Let's
  Encrypt's. Abandoned installs are swept (next section), so the ceiling is on
  installs in use, not installs ever made. Past about 2,000 of those: more
  than one zone, or install names on a DNS server of our own (which Railway
  cannot host - it has no UDP - but Fly.io can).
- **Porkbun's general request budget is 20 per 2 seconds per API key**, from
  the same spec - and "not being enforced yet": responses carry
  `X-RateLimit-Mode: observe`, and enforcement is to be announced before it
  starts. An install start costs one lookup when its address has not changed
  (a create or edit when it has), and a renewal about three calls. A refusal is
  a 429 with `Retry-After`; the service passes it on as a 502 and the install
  retries in five minutes.
- **The service caps challenges** at ten per install and three hundred overall
  per day, in memory, so a restart forgets them.

## Abandoned installs are swept, and come back by themselves

Every record the service writes carries the date its install was last heard
from, in Porkbun's notes field - stored with the record, never served in DNS.
A running install re-announces twice a day, and the date is refreshed when it
is a month old, which costs about one extra call per install per month.

Once a day the service deletes install records not heard from in 180 days
(`NAMES_FORGET_AFTER_DAYS`, at least 30), and challenge records left over for
a week by an install that crashed mid-renewal. Nothing needs undoing when such
a server comes back: its registration is not a record but an id and a token,
valid for ever, so its first announce recreates the record under **the same
name**, and a certificate that expired meanwhile renews within a minute.

It only ever touches names shaped exactly like an install's under
`home.soundstorm.dev` - never the apex, a website, `names`, or anything else -
and skips any record without a date, such as one written before dates
existed; that install's next announce dates it. It **refuses to run** if it
would delete more than a quarter of all installs at once (or twenty, for a
young service): that is far likelier a clock or a bug than a mass exodus, and
being wrong would cost everybody's name together. The refusal is logged as
`sweep refused`; look before overriding anything.
