#!/bin/sh
# Creates a new app from this skill's template.
#   scaffold.sh <target-dir> <app-name> <go-module-path>
# Example: scaffold.sh ./shop shop github.com/acme/shop
set -eu
if [ $# -ne 3 ]; then
  echo "usage: $0 <target-dir> <app-name> <go-module-path>" >&2
  exit 2
fi
target=$1 app=$2 module=$3
case $app in
  *[!a-z0-9_-]*|"") echo "app name must be lowercase letters, digits, - or _" >&2; exit 2 ;;
esac
here=$(cd "$(dirname "$0")" && pwd)
if [ -e "$target" ] && [ -n "$(ls -A "$target" 2>/dev/null)" ]; then
  echo "$target exists and is not empty" >&2
  exit 1
fi
mkdir -p "$target"
cp -R "$here/_template/." "$target/"
mv "$target/backend/go.mod.tmpl" "$target/backend/go.mod"
find "$target" -type f | while read -r f; do
  APP="$app" MODULE="$module" perl -pi -e 's/__APP__/$ENV{APP}/g; s/__MODULE__/$ENV{MODULE}/g' "$f"
done
cp "$target/.env.example" "$target/.env"
(cd "$target/backend" && go mod tidy)
echo "Created $app in $target. Next: make db && make backend, and make admin in another terminal."
