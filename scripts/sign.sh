#!/bin/sh
# Signs nexora-addon.json. Notif is an official addon, so a release carries
# Nexora's signature, made on the owner's machine only: the private key never
# goes to CI, and the release workflow refuses a manifest Nexora did not sign.
#
#   scripts/sign.sh          sign in place with NEXORA_ADDON_KEY
#                            (default ../nexora-panel/.secrets/addon_ed25519.key)
#   scripts/sign.sh -dev     write signed.json with the development key, for a
#                            panel built with -tags addondev:
#                            NEXORA_MANIFEST_FILE=signed.json go run .
#
# The signature covers every field, the version included: any edit to the
# manifest means signing again.
set -eu
cd "$(dirname "$0")/.."
kit() { go run github.com/nexora-vpn/addon-kit/cmd/nexora-addon "$@"; }

if [ "${1:-}" = "-dev" ]; then
	kit sign -dev nexora-addon.json >signed.json
	kit verify signed.json
	exit 0
fi

KEY="${NEXORA_ADDON_KEY:-../nexora-panel/.secrets/addon_ed25519.key}"
[ -f "$KEY" ] || { echo "sign: no key at $KEY (set NEXORA_ADDON_KEY)" >&2; exit 1; }
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
kit sign -key "$KEY" nexora-addon.json >"$tmp"
[ "$(kit verify "$tmp")" = "signed by: Nexora" ] || { echo "sign: $KEY is not Nexora's addon key" >&2; exit 1; }
mv "$tmp" nexora-addon.json
trap - EXIT
echo "nexora-addon.json signed by Nexora"
