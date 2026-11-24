#!/usr/bin/env bash
# Renders the icons of the novel / comic switch (96x96 PNG) from the SVGs next to this script
# into src/static/mode. Needs Chrome and ImageMagick: ./render.sh
set -euo pipefail
cd "$(dirname "$0")"
out=../../src/static/mode
chrome=${CHROME:-google-chrome}
tmp=$(mktemp -d)
for name in novel comic swap; do
  cp "$name.svg" "$tmp/$name.svg"
  echo "<body style='margin:0'><img src='$name.svg' width='576' height='576'></body>" >"$tmp/$name.html"
  "$chrome" --headless=new --no-sandbox --hide-scrollbars --user-data-dir="$tmp/profile" \
    --default-background-color=00000000 --window-size=576,576 \
    --screenshot="$tmp/$name.png" "file://$tmp/$name.html" >/dev/null 2>&1
  convert "$tmp/$name.png" -crop 576x576+0+0 +repage -resize 96x96 -strip "$out/$name.png"
done
rm -rf "$tmp"
