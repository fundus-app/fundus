#!/bin/sh
# make-icons.sh — derive all app icons from one 1024×1024 PNG.
#   scripts/make-icons.sh design/logo/source-flat.png
# Writes: app/assets/icon/app_icon.png (1024), app/web/icons/Icon-192.png,
# Icon-512.png, Icon-maskable-192.png, Icon-maskable-512.png, app/web/favicon.png (64).
# Requires ImageMagick (magick or convert).
set -e
SRC="$1"
[ -f "$SRC" ] || { echo "usage: $0 <icon-1024.png>"; exit 1; }
IM="$(command -v magick || command -v convert)"
[ -n "$IM" ] || { echo "ImageMagick not found"; exit 1; }
mkdir -p app/assets/icon app/web/icons design/logo
"$IM" "$SRC" -resize 1024x1024 app/assets/icon/app_icon.png
for s in 192 512; do
  "$IM" "$SRC" -resize ${s}x${s} "app/web/icons/Icon-$s.png"
  # maskable: same art with 10% safe padding on a paper background
  "$IM" "$SRC" -resize $((s*8/10))x$((s*8/10)) -background '#f6f2ea' -gravity center -extent ${s}x${s} "app/web/icons/Icon-maskable-$s.png"
done
"$IM" "$SRC" -resize 64x64 app/web/favicon.png
echo "icons written"
