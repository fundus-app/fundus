#!/bin/sh
# appimage.sh — package the Linux desktop app plus the daemon as one AppImage.
#   scripts/appimage.sh [VERSION] [path/to/appimagetool]
# Needs: app/build/linux/x64/release/bundle (make ui-linux), bin/fundus (make build),
# and appimagetool (downloaded to the scratch dir when not given). Output: dist/Fundus-<version>-x86_64.AppImage
set -e
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
TOOL="${2:-}"
BUNDLE=app/build/linux/x64/release/bundle
[ -x "$BUNDLE/fundus_app" ] || { echo "no Linux bundle; run: make ui-linux"; exit 1; }
[ -x bin/fundus ] || { echo "no daemon binary; run: make build"; exit 1; }
rm -rf AppDir && mkdir -p AppDir/usr/bin AppDir/usr/share/applications AppDir/usr/share/icons/hicolor/512x512/apps dist
cp -r "$BUNDLE"/. AppDir/usr/bin/
cp bin/fundus AppDir/usr/bin/fundus
cp app/assets/icon/app_icon.png AppDir/usr/share/icons/hicolor/512x512/apps/fundus.png
cp app/assets/icon/app_icon.png AppDir/fundus.png
cat > AppDir/fundus.desktop <<DESKTOP
[Desktop Entry]
Type=Application
Name=Fundus
Comment=Capture anything. Let your AI maintain the rest.
Exec=fundus_app
Icon=fundus
Categories=Office;Utility;
Terminal=false
DESKTOP
cp AppDir/fundus.desktop AppDir/usr/share/applications/
cat > AppDir/AppRun <<'APPRUN'
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
export PATH="$HERE/usr/bin:$PATH"
exec "$HERE/usr/bin/fundus_app" "$@"
APPRUN
chmod +x AppDir/AppRun
if [ -z "$TOOL" ]; then
  TOOL="${TMPDIR:-/tmp}/appimagetool-1.9.0"
  [ -x "$TOOL" ] || { curl -sSL -o "$TOOL" https://github.com/AppImage/appimagetool/releases/download/1.9.0/appimagetool-x86_64.AppImage && chmod +x "$TOOL"; }
fi
ARCH=x86_64 "$TOOL" --appimage-extract-and-run AppDir "dist/Fundus-${VERSION}-x86_64.AppImage" >/dev/null 2>&1 || ARCH=x86_64 "$TOOL" AppDir "dist/Fundus-${VERSION}-x86_64.AppImage"
rm -rf AppDir
ls -la "dist/Fundus-${VERSION}-x86_64.AppImage"
