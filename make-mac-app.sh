#!/bin/sh
# Build Mirador and package it as a macOS .app bundle.
#
# Usage:
#   ./make-mac-app.sh               # Apple Silicon, version 1.0.0
#   ./make-mac-app.sh amd64         # Intel Mac, version 1.0.0
#   ./make-mac-app.sh arm64 1.1.0   # Apple Silicon, custom version
#
# Requires: Go and Xcode Command Line Tools (xcode-select --install)

set -e

ARCH="${1:-arm64}"
VERSION="${2:-1.0.0}"
APP_NAME="Mirador"

case "$ARCH" in
  arm64|amd64) ;;
  *)
    echo "Usage: $0 [arm64|amd64] [version]" >&2
    exit 1
    ;;
esac

echo "==> Building mirador (darwin/$ARCH, version $VERSION)"
GOOS=darwin GOARCH="$ARCH" go build \
  -ldflags "-X main.version=$VERSION" \
  -o mirador-build .

echo "==> Creating $APP_NAME.app"
rm -rf "$APP_NAME.app"
mkdir -p "$APP_NAME.app/Contents/MacOS" "$APP_NAME.app/Contents/Resources"

cp mirador-build "$APP_NAME.app/Contents/MacOS/mirador"
chmod +x "$APP_NAME.app/Contents/MacOS/mirador"

cat > "$APP_NAME.app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>mirador</string>
	<key>CFBundleIdentifier</key><string>com.jplassnibatt.mirador</string>
	<key>CFBundleName</key><string>Mirador</string>
	<key>CFBundleDisplayName</key><string>Mirador</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleVersion</key><string>$VERSION</string>
	<key>CFBundleShortVersionString</key><string>$VERSION</string>
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

echo "==> Ad-hoc signing $APP_NAME.app"
codesign --sign - --force --deep "$APP_NAME.app" 2>/dev/null || true

rm mirador-build

echo "==> Done: $APP_NAME.app"
echo "    Double-click it, then open http://127.0.0.1:8080"
