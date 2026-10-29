#!/bin/sh
# Signs nx-addon.json. Notif is an official addon, so a release carries
# NX's signature, made on the owner's machine only: the private key never
# goes to CI, and the release workflow refuses a manifest NX did not sign.
#
#   scripts/sign.sh          sign in place with NX_ADDON_KEY
#                            (default ../nx-panel/.secrets/addon_ed25519.key)
#   scripts/sign.sh -dev     write signed.json with the development key, for a
#                            panel built with -tags addondev:
#                            NX_MANIFEST_FILE=signed.json go run .
#
# The signature covers every field, the version included: any edit to the
# manifest means signing again.
set -eu
cd "$(dirname "$0")/.."
kit() { go run github.com/nx-panel/addon-kit/cmd/nx-addon "$@"; }

if [ "${1:-}" = "-dev" ]; then
	kit sign -dev nx-addon.json >signed.json
	kit verify signed.json
	exit 0
fi

KEY="${NX_ADDON_KEY:-../nx-panel/.secrets/addon_ed25519.key}"
[ -f "$KEY" ] || { echo "sign: no key at $KEY (set NX_ADDON_KEY)" >&2; exit 1; }
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
kit sign -key "$KEY" nx-addon.json >"$tmp"
[ "$(kit verify "$tmp")" = "signed by: NX" ] || { echo "sign: $KEY is not NX's addon key" >&2; exit 1; }
mv "$tmp" nx-addon.json
trap - EXIT
echo "nx-addon.json signed by NX"
