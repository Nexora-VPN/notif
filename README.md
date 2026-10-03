# Nexora Notif

An official, open [Nexora](https://nexora-panel.org) addon that tells a
panel's users about their accounts: the expiry date drawing near and the
traffic running out, renewals and other changes, and messages of the admin's
own to one user or a group — over Telegram, Bale, SMS, email or any HTTP API.
Each notice is written once and goes through the first of the user's
channels that reaches them, in the order the admin sets, so a paid SMS is
sent only when the free channels could not deliver.

> **Status: in development.** This is the skeleton — registration with a
> panel, the admin web with two-factor sign-in, SQLite or PostgreSQL. The
> messengers, notices and messages arrive in the next versions.

Notif is separate software, as every Nexora addon is: its own container or
service and its own database, talking to the panel over the network only,
with a scoped token and the panel's signed events. It keeps no copy of the
panel's data beyond what it needs to send.

## Install

From the panel: **Services → Addons → Browse → Nexora Notif → Install**. The
panel asks the manifest's questions (port, database, the first admin) and
installs Notif on its own host or another one over SSH, or prints the
command to run by hand:

```sh
sh install.sh --method docker --opt port=8097 --opt admin_password=… \
  --panel-url https://panel.example --claim-code …
```

`--method script` installs the binary under systemd instead of Docker. Run
the same command again to update; `--uninstall [--purge]` removes it.

## Develop

```sh
cp .env.example .env
(cd frontend && npm ci && npm run build)
NEXORA_OPT_ADMIN_PASSWORD=change-me-please go run .
# admin web on http://localhost:8097
```

A panel registers only a manifest signed by a key it trusts. For a local
try, sign with the development key and run a panel built with
`-tags addondev`:

```sh
scripts/sign.sh -dev
NEXORA_MANIFEST_FILE=signed.json go run .
```

Tests run on SQLite, and on PostgreSQL with `NEXORA_TEST_POSTGRES_DSN`.

Built on [addon-kit](https://github.com/Nexora-VPN/addon-kit). Licensed
under the GNU Affero General Public License v3.0: a changed Notif offered to
others over a network must offer them its source.
