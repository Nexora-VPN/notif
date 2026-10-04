# Nexora Notif

Notif tells the users of a Nexora panel about their accounts: the expiry
drawing near, the traffic running low, a renewal, traffic added, and the
admin's own messages — over Telegram, Bale, Soroush Plus, Rubika, SMS,
email, ntfy or any HTTP API.

**One notice, one delivery.** A notice is written once and handed to the
user's channels in the order you set. The next channel is tried only when
one could not deliver: the user has no address there, the channel refused
(a blocked bot, a number that does not exist), or it failed three times.
So a paid SMS goes out only when the free channels could not reach the user,
and nobody gets the same notice twice.

Notif is a separate program, as every Nexora addon is: its own service and
database, reading the panel with a scoped token and its signed events. It
keeps every messenger link in the account's contact card on the panel, where
other addons see it too.

## Install

On the panel: **Services → Addons → Browse → Nexora Notif → Install**. The
panel asks the manifest's questions — the port (8097), the database (SQLite,
or PostgreSQL with its connection string), the first admin's name and
password — and installs Notif on its own host or another one over SSH, or
prints a command to run by hand:

```sh
sh install.sh --method docker --opt port=8097 --opt admin_password=… \
  --panel-url https://panel.example --claim-code …
```

`--method script` runs the binary under systemd instead of Docker. The same
command updates in place; `--uninstall [--purge]` removes it. Once Notif
answers its health check the panel registers it and asks you to approve its
permissions: reading accounts, and writing the messenger links into their
contact cards.

Sign in at `http://<host>:8097` with the first admin, and turn on two-factor
sign-in under **Security**. A lost password is reset on the server:
`notif admin reset-password -user admin -pass …`.

## Channels

**Channels** lists your channels in the fall-back order: the first that can
reach a user carries the notice. Move them with the arrows; switch one off
without deleting it. Each shows what it sent today and this month. **Test**
sends a test notice to one account by name.

### Telegram, Bale and Soroush Plus

The three speak one Bot API. Make a bot with each messenger's BotFather and
paste its token. **Give Notif bots of its own**: a bot's updates can be read
by one program only, so a token Shop or another tool already uses cannot be
shared (the channel says so if it is).

- **Telegram** — a server in Iran or China cannot reach `api.telegram.org`:
  set a **proxy** (`socks5://host:port`), or a Bot API mirror as the API
  address.
- **Bale** — Bale writes every message in its own Markdown, and slows a bot
  that writes to users who are not talking to it. Its paid **business API**
  is made for notices: switch it on in the channel once your Bale business
  account is funded.
- **Soroush Plus** — like Telegram, at `api.splus.ir`.

### Rubika

Rubika has a Bot API of its own, which Notif speaks. Its API may refuse
servers outside Iran: give the channel a proxy inside Iran, or the address
of a relay there.

### Linking users to a bot

A bot can only write to someone who has started it. A user links their
account by sending the bot **their subscription link** (or just its token),
or the **link code** you find on their page under **Accounts** — for
Telegram that page also gives a link that carries the code, so one tap
links. The chat is written into the account's contact card on the panel
(`telegram_id`, `bale_id`, `soroush_id`, `rubika_id`), with the user's
messenger language as `lang` when the card has none. `/stop` unlinks; a
user who blocks the bot is unlinked by itself and their notices fall to
the next channel. Five wrong tries in ten minutes lock a chat out for a
while.

### SMS: Kavenegar and Faraz SMS

Both send by an **approved template** on a service line — the kind that
reaches numbers which block advertising SMS. Write the template in the
provider's panel and have it approved; then tell Notif which of its
variables carries what, one line each:

- **Kavenegar**: the template name, and `token={name}`, `token2={days}` …
  Kavenegar's variables are `token`, `token2`, `token3` (no spaces —
  Notif turns spaces into dashes), `token10` (up to 5 spaces) and
  `token20` (up to 8). A template such as:
  `%token, your account expires in %token2 days, on %token3.`
- **Faraz SMS**: the pattern's code and your line number, the variables as
  the pattern names them (`name={name}`), and **the length each has in the
  pattern** (`name=20`). Faraz holds a message for a person to approve when
  a value is longer than the pattern says — and still answers "sent" — so
  Notif cuts every value to its length.

The phone comes from the contact card's `phone`, written any way an
operator types it (`+98 912…`, `0912…`, `912…`, Persian digits). An SMS
provider's terms may forbid promoting VPNs; Notif's notices are account
notices, and its default texts avoid the word.

### Email

Any SMTP server: host, port (587 with STARTTLS, 465 with TLS), user and
password, the sender. Mail is plain text. **Send from your own domain with
SPF, DKIM and DMARC set**: QQ Mail, 163, Mail.ru, Yandex and Gmail refuse
or bury mail they cannot verify. The address comes from the card's `email`.

### ntfy

ntfy is a push app users install themselves; each subscribes to a topic of
their own. Turn ntfy on for a user on their page under **Accounts**: Notif
draws a topic nobody can guess, keeps it in the card as `ntfy`, and shows
the address to hand them (`https://ntfy.sh/notif-…`, or your own server).
Whoever knows the address can read it — give it to that user only. On
Chinese Android phones, which have no Google services, users turn on
**instant delivery** in the app; on iOS a self-hosted server needs
`upstream-base-url` set to `https://ntfy.sh`.

### The generic HTTP channel

Any provider with an HTTP API: an address, a method, headers and a body,
each a template over the notice, and the contact field that holds the
user's address there (`phone`, or a key of your own such as `vk_id`). The
provider's key goes in **Secret** and is read as `{{.Secret}}`; it never
appears in the log. The body is JSON, a form (`name=value`, one a line) or
none; there is HTTP Basic authentication, a **success pattern** for
providers that answer 200 to a failure, and a CA certificate for a private
one.

Variables: `{{.Text}}`, `{{.Title}}`, `{{.Address}}`, `{{.Name}}`,
`{{.Secret}}`, `{{.ID}}` (a number fixed per notice — VK's `random_id`),
`{{.Key}}` (Matrix's transaction id), `{{.Vars.days}}` and the rest of the
notice's variables; functions `json`, `urlquery`, `e164` (`+98912…`),
`msisdn` (`98912…`) and `iran` (`0912…`).

**Presets** fill everything for SMS.ru, SMSC, SMS Aero, MTS Exolve, VK,
LINE, Matrix and Pushover — replace what is in CAPITALS. WhatsApp is reached
through an intermediary of your choosing on this channel; Notif ships no
unofficial WhatsApp client.

## Notices

**Notices** has your **schedule** — the days before the expiry (e.g. `5, 1`)
and the shares of traffic (e.g. `80, 95`) at which a user hears — and every
kind of notice, each on or off:

- **On your schedule**: expiry coming, traffic running low. Each line is said
  once, and re-arms when the account is renewed, traffic is added or a new
  cycle starts.
- **When the panel says**: renewed, expired, traffic used up, created (with
  the subscription link), a reseller's allowance gone, back on, plan
  started, subscription added, device limit, deleted.
- **When an admin changes an account**: traffic added, the date moved, usage
  reset, switched off or on by hand. These wait a minute and a half: when
  the change was a renewal, the renewal's notice takes their place.

**Words** opens a notice's text per language, and — when one channel needs
other words, like a short SMS — per channel kind. A field left empty keeps
the default. Click a variable to add it: `{name}`, `{expiry}`, `{days}`,
`{traffic_total}`, `{traffic_used}`, `{traffic_left}`, `{percent}`,
`{added}`, `{sub_url}`, `{group}`. The preview shows it for a sample
account. Dates are in the Persian calendar for Persian by default (or one
calendar for all, under **Dates**); figures use Persian digits in Persian.

A user's language is their card's `lang` (fa, en, ru or zh), else the
language under **Settings**. **Quiet hours** (Settings) hold a notice until
they end — nothing is dropped; a notice marked **urgent** goes through them.

## Messages

**Messages** writes to one or more accounts, or to a group the panel's own
filters pick: group, status, expiring within, traffic used, owner,
template, node, a search. **Count** shows, before anything goes, how many
accounts match, which channel would carry each, how many are SMS and how
many no channel reaches, with the message as the first account would read
it. **Send** asks once more, naming the SMS count. The message goes out at
the channels' rates in the background; what has not gone can be cancelled;
the report gives what was sent, by which channel, and what failed. An
account's page lists what it was sent.

## Iran, Russia and China

| | Reaches a user with the VPN on | Reaches a user with the VPN off |
| --- | --- | --- |
| Iran | Telegram, every other channel | Bale, Soroush Plus, Rubika, SMS, email |
| Russia | Telegram, email, ntfy | email (Mail.ru and Yandex stay open in the mobile whitelist mode); SMS for an operator with a Russian legal entity |
| China | Telegram, ntfy, email | email (QQ Mail, 163) |

A good order puts the free channel users read first and SMS or email last:
in Iran, Telegram → Bale → SMS; in Russia and China, Telegram → ntfy →
email. Russia fines advertising a way around blocking (38-FZ): keep notices
about the account — the default texts carry no prices, offers or the word
VPN. Russian SMS providers give a sender name only to a Russian company or
sole trader; China's SMS routes need a mainland company — Notif names none.

## The log and privacy

**Log** lists every delivery with each channel's attempt and its answer.
Finished deliveries, with their text, are deleted after the days set under
**Settings** (90 by default). Notif keeps a copy of the accounts' name,
contact card, expiry and traffic to decide and write notices; the
subscription link's secret part is kept only as a hash.

## Backup

On SQLite Notif copies its database once a day to `<data>/backups`, keeping
seven. By hand: `notif backup -o notif.db`, and with Notif stopped,
`notif restore -i notif.db` (the replaced database is kept beside it). On
PostgreSQL back the database up with `pg_dump`.

## When something is wrong

- **A bot channel says another program reads its updates** — the token is
  in use elsewhere (Shop, a webhook): give Notif its own bot.
- **"No address" in the log** — the user has nothing for that channel in
  their card; they fall to the next channel.
- **"No channel reached the user"** — every channel was tried; the log's
  attempts say why each failed.
- **Nothing queued for an account** — no channel that is on has an address
  for it: link a bot, or add a phone or email to the card.
