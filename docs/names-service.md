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

1. **New service → GitHub repo** → this repository.
2. **Settings → Config file path:** `cmd/soundstorm-names/railway.toml`. That
   points the build at the service's own Dockerfile rather than SoundStorm's.
3. **Variables:**

   | Variable | Value |
   | --- | --- |
   | `NAMES_SECRET` | 48 random bytes: `openssl rand -base64 48`. Keep a copy somewhere safe - changing it makes every install register again. |
   | `PORKBUN_API_KEY` | the `pk1_...` half |
   | `PORKBUN_SECRET_API_KEY` | the `sk1_...` half |
   | `NAMES_CLIENT_IP_HEADER` | `X-Real-IP` - the header Railway's proxy puts the caller's address in, used for rate limiting. **Unverified**; if registration starts refusing everybody at once, this is why. |

4. **Settings → Networking → Custom domain:** `names.soundstorm.dev`. Railway
   shows a CNAME target; add that record at Porkbun (**DNS** for
   `soundstorm.dev`, type CNAME, host `names`). Railway issues the
   certificate for it by itself.

Check it: `https://names.soundstorm.dev/healthz` answers `{"status":"ok"}`.

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
- **Porkbun's API limits are not documented here** because they were not
  checked. The service makes one lookup per install start and a write only
  when something changed.
- **The service caps challenges** at ten per install and three hundred overall
  per day, in memory, so a restart forgets them.
