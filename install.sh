#!/bin/sh
# Installs, updates or removes this Nexora addon on a Linux host — by hand, or
# by the panel over SSH (the same way it installs a node). Run it as root.
#
#   sh install.sh --method docker --opt port=8097 --panel-url https://panel.example --claim-code ABC123
#   sh install.sh --method script --version v0.1.0 --opt base_path=k3x9q2
#   sh install.sh --uninstall [--purge]
#
# --method script   the binary under a systemd unit (no Docker needed)
# --method docker   the compose stack in deploy/compose.yml
# --version TAG     a release tag (default: the latest release)
# --binary-file F   install this archive instead of downloading one (script)
# --sha256 HEX      the archive's SHA-256, checked before it is installed: the
#                   panel hands it from the release's signed SHA256SUMS
# --opt KEY=VALUE   an answer to one of the manifest's install options; repeat.
#                   A first install with no base_path draws one;
#                   base_path= puts Notif at the root.
# --panel-url URL   the panel's address, passed as NEXORA_PANEL_URL
# --claim-code C    the one-time code the panel registers the addon with
# --uninstall       stop and remove the addon; --purge also deletes its data
#
# Running it again updates in place: the answers already given are kept
# unless --opt changes them, and the data directory is never touched.
set -eu

SLUG="notif"
REPO="Nexora-VPN/notif"
BIN="notif"
DIR="/opt/nexora-addons/${SLUG}"
UNIT="nexora-addon-${SLUG}"

METHOD=""
VERSION=""
BINARY_FILE=""
SHA256=""
PANEL_URL=""
CLAIM_CODE=""
UNINSTALL=0
PURGE=0
OPTS=""

die() { echo "install: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--method) METHOD="$2"; shift 2 ;;
	--version) VERSION="$2"; shift 2 ;;
	--binary-file) BINARY_FILE="$2"; shift 2 ;;
	--sha256) SHA256="$2"; shift 2 ;;
	--opt)
		case "$2" in *=*) ;; *) die "--opt takes KEY=VALUE" ;; esac
		OPTS="${OPTS}$2
"
		shift 2 ;;
	--panel-url) PANEL_URL="$2"; shift 2 ;;
	--claim-code) CLAIM_CODE="$2"; shift 2 ;;
	--uninstall) UNINSTALL=1; shift ;;
	--purge) PURGE=1; shift ;;
	*) die "unknown argument $1" ;;
	esac
done

[ "$(id -u)" = 0 ] || die "run as root"

if [ "$UNINSTALL" = 1 ]; then
	if [ -f "/etc/systemd/system/${UNIT}.service" ]; then
		systemctl disable --now "$UNIT" 2>/dev/null || true
		rm -f "/etc/systemd/system/${UNIT}.service"
		systemctl daemon-reload
	fi
	if [ "$PURGE" = 1 ] && id "nexora-${SLUG}" >/dev/null 2>&1; then
		userdel "nexora-${SLUG}" 2>/dev/null || deluser "nexora-${SLUG}" 2>/dev/null || true
	fi
	if [ -f "${DIR}/compose.yml" ] && command -v docker >/dev/null 2>&1; then
		docker compose -f "${DIR}/compose.yml" --env-file "${DIR}/.env" down || true
	fi
	if [ "$PURGE" = 1 ]; then
		rm -rf "$DIR"
	else
		rm -rf "${DIR}/bin" "${DIR}/compose.yml"
	fi
	echo "removed ${SLUG}"
	exit 0
fi

# The admin's password, when the command gives one, held to the rule
# Notif's sign-in holds it to — 10 characters to 72 bytes — here, before
# anything is installed: Notif would refuse it at its start and not start.
pw="$(printf '%s' "$OPTS" | sed -n 's/^admin_password=//p' | tail -n 1)"
if [ -n "$pw" ]; then
	n=$(printf '%s' "$pw" | LC_ALL=C tr -d '\200-\277' | wc -c)
	[ $((n)) -ge 10 ] || die "the admin password is shorter than 10 characters"
	n=$(printf '%s' "$pw" | wc -c)
	[ $((n)) -le 72 ] || die "the admin password is longer than 72 bytes (about 36 Persian or Russian letters)"
fi

# An update keeps the method the addon was installed with.
if [ -z "$METHOD" ] && [ -f "${DIR}/.method" ]; then
	METHOD="$(cat "${DIR}/.method")"
fi
case "$METHOD" in
script | docker) ;;
*) die "--method script or --method docker" ;;
esac

if [ -z "$VERSION" ]; then
	VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
	[ -n "$VERSION" ] || die "could not read the latest release of ${REPO}"
fi

# A first install, not an update: no answers written yet.
FRESH=1
[ ! -f "${DIR}/.env" ] || FRESH=0

mkdir -p "${DIR}/data"
chmod 700 "${DIR}"

# The answers go to a copy of the .env, which takes its place only once
# the checks below pass: a command refused leaves the install as it was.
ENVF="${DIR}/.env.new"
rm -f "$ENVF" "${ENVF}.tmp"
trap 'rm -f "${DIR}/.env.new" "${DIR}/.env.new.tmp"' EXIT
[ ! -f "${DIR}/.env" ] || cp -p "${DIR}/.env" "$ENVF"

# envquote VALUE: the value as both readers of the .env take it literally —
# systemd's EnvironmentFile and docker compose's env_file. Single quotes are
# literal to both; a value holding one goes in double quotes, with \ and "
# escaped, and for compose, which expands $ inside them, $ doubled.
envquote() {
	case "$1" in
	*\'*) ;;
	*)
		printf "'%s'" "$1"
		return
		;;
	esac
	v="$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
	[ "$METHOD" != docker ] || v="$(printf '%s' "$v" | sed 's/\$/$$/g')"
	printf '"%s"' "$v"
}

# setenv KEY VALUE: replace or add one line of the .env file.
setenv() {
	touch "$ENVF"
	chmod 600 "$ENVF"
	grep -v "^$1=" "$ENVF" >"${ENVF}.tmp" || true
	printf '%s=%s\n' "$1" "$(envquote "$2")" >>"${ENVF}.tmp"
	mv "${ENVF}.tmp" "$ENVF"
	chmod 600 "$ENVF" # the mv carries the temporary file's mode, not the 600 above
}

printf '%s' "$OPTS" | while IFS= read -r kv; do
	[ -n "$kv" ] || continue
	key="$(printf '%s' "${kv%%=*}" | tr 'a-z' 'A-Z')"
	setenv "NEXORA_OPT_${key}" "${kv#*=}"
done
[ -z "$PANEL_URL" ] || setenv NEXORA_PANEL_URL "$PANEL_URL"
[ -z "$CLAIM_CODE" ] || setenv NEXORA_CLAIM_CODE "$CLAIM_CODE"
setenv NEXORA_ADDON_VERSION "${VERSION#v}"

# unsetenv KEY: drop a line of the .env file.
unsetenv() {
	[ -f "$ENVF" ] || return 0
	grep -v "^$1=" "$ENVF" >"${ENVF}.tmp" || true
	mv "${ENVF}.tmp" "$ENVF"
	chmod 600 "$ENVF"
}

# getenv KEY: a value of the .env, its quotes taken off (setenv's quoting
# of a value with no quote in it).
getenv() {
	sed -n "s/^$1=//p" "$ENVF" 2>/dev/null | tail -n 1 | sed -e "s/^'\(.*\)'\$/\1/" -e 's/^"\(.*\)"$/\1/'
}

# The base path everything is served under: a first install the command
# gives none draws one, so Notif is not where a scanner looks; an update
# keeps what the install has — none, for one from before base paths, is
# the root.
if [ "$FRESH" = 1 ] && ! grep -q '^NEXORA_OPT_BASE_PATH=' "$ENVF"; then
	setenv NEXORA_OPT_BASE_PATH "$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 12)"
fi

# With HTTPS on, Notif's port is its HTTPS port and nothing plain listens
# (one address, docs/phase-h.md P9 in the panel's repository): the public
# address names that port (443 when it names none). acme answers the CA on
# it, so it is 443; acme-http answers the CA on port 80 as well, which
# compose publishes only then. An update of an install from before keeps
# its two ports: HTTPS on a listener of its own beside the plain one.
HTTPS_MODE="$(getenv NEXORA_OPT_HTTPS)"
PORT_NOW="$(getenv NEXORA_OPT_PORT)"
PORT_NOW="${PORT_NOW:-8097}"
TWO_PORTS=0
if [ -z "$CLAIM_CODE" ] && [ "$FRESH" = 0 ]; then
	[ -z "$(getenv NEXORA_HTTPS_LISTEN)" ] || TWO_PORTS=1
	case "$(getenv NEXORA_HTTPS_PUBLISH)" in "" | 127.0.0.1:*) ;; *) TWO_PORTS=1 ;; esac
fi
if [ "$TWO_PORTS" = 1 ]; then
	# Named in .env: the compose file sets the listener from it, empty
	# (one port) when .env names none, whatever the image's :8443.
	[ "$METHOD" != docker ] || [ -n "$(getenv NEXORA_HTTPS_LISTEN)" ] || setenv NEXORA_HTTPS_LISTEN ":8443"
else
	# Empty, not absent: the image's own :8443 is for an install from before.
	setenv NEXORA_HTTPS_LISTEN ""
	unsetenv NEXORA_HTTPS_PUBLISH
	case "$HTTPS_MODE" in
	panel | acme | acme-http | self-signed)
		PUBLIC="$(getenv NEXORA_OPT_PUBLIC_URL)"
		case "$PUBLIC" in https://*) ;; *) die "https ${HTTPS_MODE} serves the public address over HTTPS: it starts with https://" ;; esac
		p="$(printf '%s' "$PUBLIC" | sed -n 's#^https://[^/]*:\([0-9][0-9]*\)\(/.*\)\{0,1\}$#\1#p')"
		[ -n "$p" ] || p=443
		[ "$p" = "$PORT_NOW" ] || die "Notif serves HTTPS on its port ${PORT_NOW}: the public address must name it (https://<host>:${PORT_NOW}), or give Notif port ${p}"
		[ "$HTTPS_MODE" != acme ] || [ "$PORT_NOW" = 443 ] || die "acme answers the CA on port 443: give Notif port 443, or choose acme-http or panel"
		# acme-http holds port 80 — 8080 in the container, where Notif listens too.
		[ "$HTTPS_MODE" != acme-http ] || [ "$PORT_NOW" != 80 ] || die "acme-http answers the CA on port 80: give Notif another port"
		[ "$HTTPS_MODE" != acme-http ] || [ "$METHOD" != docker ] || [ "$PORT_NOW" != 8080 ] || die "acme-http answers the CA on port 8080 inside the container: give Notif another port"
		;;
	esac
fi
case "$HTTPS_MODE" in
acme-http) setenv NEXORA_HTTP_PUBLISH "80:8080" ;;
*) setenv NEXORA_HTTP_PUBLISH "127.0.0.1::8080" ;;
esac

# Checked: the answers are the install's.
mv "$ENVF" "${DIR}/.env"
ENVF="${DIR}/.env"
echo "$METHOD" >"${DIR}/.method"

case "$METHOD" in
script)
	case "$(uname -m)" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) die "no build of ${SLUG} for $(uname -m)" ;;
	esac
	setenv NEXORA_DATA_DIR "${DIR}/data"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	ARCHIVE="${BIN}-linux-${ARCH}.tar.gz"
	if [ -n "$BINARY_FILE" ]; then
		# An archive the operator brought is theirs to vouch for.
		cp "$BINARY_FILE" "${tmp}/${ARCHIVE}"
	else
		base="https://github.com/${REPO}/releases/download/${VERSION}"
		curl -fsSL -o "${tmp}/${ARCHIVE}" "${base}/${ARCHIVE}"
		# The release's checksums, or nothing is installed.
		curl -fsSL -o "${tmp}/SHA256SUMS" "${base}/SHA256SUMS" || die "the release ${VERSION} has no SHA256SUMS; not installing an archive that cannot be checked"
		grep " ${ARCHIVE}\$" "${tmp}/SHA256SUMS" >"${tmp}/expected" || die "SHA256SUMS of ${VERSION} does not list ${ARCHIVE}"
		(cd "$tmp" && sha256sum -c expected >/dev/null) || die "${ARCHIVE} does not match the release's SHA256SUMS"
	fi
	if [ -n "$SHA256" ]; then
		if command -v sha256sum >/dev/null 2>&1; then
			got="$(sha256sum "${tmp}/${ARCHIVE}" | cut -d' ' -f1)"
		else
			got="$(shasum -a 256 "${tmp}/${ARCHIVE}" | cut -d' ' -f1)"
		fi
		[ "$got" = "$(printf '%s' "$SHA256" | tr 'A-F' 'a-f')" ] || die "the archive's SHA-256 is ${got}, not the ${SHA256} the release signed"
	fi
	tar -xzf "${tmp}/${ARCHIVE}" -C "$tmp"
	mkdir -p "${DIR}/bin"
	install -m 755 "${tmp}/${BIN}" "${DIR}/bin/${BIN}"
	# Its own user, owning only the data: the service reads its settings
	# and writes its database, nothing else of the host.
	if ! id "nexora-${SLUG}" >/dev/null 2>&1; then
		useradd --system --no-create-home --home-dir "${DIR}" --shell /usr/sbin/nologin "nexora-${SLUG}" 2>/dev/null ||
			adduser -S -H -h "${DIR}" -s /sbin/nologin "nexora-${SLUG}" ||
			die "could not make the user nexora-${SLUG}"
	fi
	chown -R "nexora-${SLUG}" "${DIR}/data"
	chmod 700 "${DIR}/data"
	chown "nexora-${SLUG}" "${DIR}/.env"
	chmod 755 "${DIR}" # the service user passes through to its data and .env
	cat >"/etc/systemd/system/${UNIT}.service" <<UNIT
[Unit]
Description=Nexora addon ${SLUG}
After=network-online.target
Wants=network-online.target

[Service]
User=nexora-${SLUG}
EnvironmentFile=${DIR}/.env
WorkingDirectory=${DIR}
ExecStart=${DIR}/bin/${BIN}
Restart=always
RestartSec=3
# Time for the sends in hand to finish on a stop (Notif waits up to 45s).
TimeoutStopSec=60
# A port under 1024 is the one privilege it keeps.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
ReadWritePaths=${DIR}/data

[Install]
WantedBy=multi-user.target
UNIT
	systemctl daemon-reload
	systemctl enable "$UNIT" >/dev/null 2>&1
	systemctl restart "$UNIT"
	;;
docker)
	command -v docker >/dev/null 2>&1 || die "docker is not installed; use --method script"
	chown 10001 "${DIR}/data" # the image runs as uid 10001
	curl -fsSL -o "${DIR}/compose.yml" "https://raw.githubusercontent.com/${REPO}/${VERSION}/deploy/compose.yml"
	docker compose -f "${DIR}/compose.yml" --env-file "${DIR}/.env" pull
	docker compose -f "${DIR}/compose.yml" --env-file "${DIR}/.env" up -d
	;;
esac

echo "installed ${SLUG} ${VERSION} (${METHOD}) in ${DIR}"
BASE="$(getenv NEXORA_OPT_BASE_PATH | tr -d /)"
PORT="$(getenv NEXORA_OPT_PORT)"
# With HTTPS on one port, the admin is at the public address.
ADMIN="http://<this host>:${PORT:-8097}"
if [ "$TWO_PORTS" = 0 ]; then
	case "$HTTPS_MODE" in panel | acme | acme-http | self-signed) ADMIN="$(getenv NEXORA_OPT_PUBLIC_URL | sed 's#/*$##')" ;; esac
fi
echo "the admin: ${ADMIN}/${BASE}${BASE:+/ — keep the path to yourself}"
