#!/usr/bin/env bash
# Renders the tab bar icons (81x81 PNG, the size WeChat recommends) from the SVGs next to this
# script into src/static/tabbar. Needs Chrome and ImageMagick: ./render.sh
set -euo pipefail
cd "$(dirname "$0")"
out=../../src/static/tabbar
chrome=${CHROME:-google-chrome}
tmp=$(mktemp -d)
for name in shelf search mine; do
  cp "$name.svg" "$tmp/$name.svg"
  # The selected icon takes the brand colour and a light fill.
  sed -e 's/#8a8a8a/#c0392b/' -e 's/class="fill"/fill="#c0392b" fill-opacity="0.16"/g' "$name.svg" >"$tmp/$name-active.svg"
  for icon in "$name" "$name-active"; do
    echo "<body style='margin:0'><img src='$icon.svg' width='648' height='648'></body>" >"$tmp/$icon.html"
    "$chrome" --headless=new --no-sandbox --hide-scrollbars --user-data-dir="$tmp/profile" \
      --default-background-color=00000000 --window-size=648,648 \
      --screenshot="$tmp/$icon.png" "file://$tmp/$icon.html" >/dev/null 2>&1
    convert "$tmp/$icon.png" -crop 648x648+0+0 +repage -resize 81x81 -strip "$out/$icon.png"
  done
done
rm -rf "$tmp"
