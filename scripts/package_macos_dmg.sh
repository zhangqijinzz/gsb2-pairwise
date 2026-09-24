#!/usr/bin/env bash

# Assemble the macOS .app bundle and wrap it in a distributable DMG.
#
# Expects the Go binary to already exist (build it with `go build -o build/bin/pinru .`
# after `cd frontend && npm run build`, or via the Taskfile `dmg` task).
#
# Environment overrides:
#   APP_NAME, ARCHIVE_SUFFIX, OUTPUT_DIR, BINARY_PATH, INFO_PLIST, ICON_PATH,
#   APP_BUNDLE, DMG_PATH, VOLUME_NAME, APPLE_SIGNING_IDENTITY

set -euo pipefail

APP_NAME="${APP_NAME:-PINRU}"
ARCHIVE_SUFFIX="${ARCHIVE_SUFFIX:-macos-arm64}"
OUTPUT_DIR="${OUTPUT_DIR:-dist}"
BINARY_PATH="${BINARY_PATH:-build/bin/pinru}"
INFO_PLIST="${INFO_PLIST:-build/darwin/Info.plist}"
ICON_PATH="${ICON_PATH:-build/darwin/icons.icns}"
ENTITLEMENTS_PATH="${ENTITLEMENTS_PATH:-build/darwin/entitlements.plist}"
APP_BUNDLE="${APP_BUNDLE:-${OUTPUT_DIR}/${APP_NAME}.app}"
DMG_PATH="${DMG_PATH:-${OUTPUT_DIR}/${APP_NAME}-${ARCHIVE_SUFFIX}-$(date +%Y%m%d-%H%M%S).dmg}"
VOLUME_NAME="${VOLUME_NAME:-${APP_NAME}}"
SIGN_IDENTITY="${APPLE_SIGNING_IDENTITY:-}"
STAGING_DIR="${STAGING_DIR:-${OUTPUT_DIR}/.${APP_NAME}-dmg-staging}"

require_file() {
  local path="$1"
  if [[ ! -e "$path" ]]; then
    echo "missing required file: $path" >&2
    exit 1
  fi
}

require_file "$BINARY_PATH"
require_file "$INFO_PLIST"
require_file "$ICON_PATH"

if [[ -n "$SIGN_IDENTITY" ]]; then
  require_file "$ENTITLEMENTS_PATH"
fi

echo "==> assembling ${APP_BUNDLE}"
rm -rf "$APP_BUNDLE"
mkdir -p "${APP_BUNDLE}/Contents/MacOS" "${APP_BUNDLE}/Contents/Resources"

cp "$BINARY_PATH" "${APP_BUNDLE}/Contents/MacOS/pinru"
chmod +x "${APP_BUNDLE}/Contents/MacOS/pinru"
cp "$INFO_PLIST" "${APP_BUNDLE}/Contents/Info.plist"
cp "$ICON_PATH" "${APP_BUNDLE}/Contents/Resources/icons.icns"

xattr -cr "$APP_BUNDLE"
plutil -lint "${APP_BUNDLE}/Contents/Info.plist"

if [[ -n "$SIGN_IDENTITY" ]]; then
  echo "==> signing with ${SIGN_IDENTITY}"
  codesign \
    --force \
    --deep \
    --options runtime \
    --entitlements "$ENTITLEMENTS_PATH" \
    --sign "$SIGN_IDENTITY" \
    "$APP_BUNDLE"
else
  echo "warning: APPLE_SIGNING_IDENTITY is empty; applying an ad-hoc signature" >&2
  codesign --force --deep --sign - "$APP_BUNDLE"
fi

codesign --verify --deep --strict --verbose=2 "$APP_BUNDLE"

echo "==> staging DMG contents"
rm -rf "$STAGING_DIR"
mkdir -p "$STAGING_DIR"
cp -R "$APP_BUNDLE" "${STAGING_DIR}/"
ln -s /Applications "${STAGING_DIR}/Applications"

echo "==> creating ${DMG_PATH}"
rm -f "$DMG_PATH"
hdiutil create \
  -volname "$VOLUME_NAME" \
  -srcfolder "$STAGING_DIR" \
  -ov \
  -format UDZO \
  "$DMG_PATH"

hdiutil verify "$DMG_PATH"
rm -rf "$STAGING_DIR"

echo "created: $DMG_PATH"
