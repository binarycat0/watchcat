#!/bin/sh
# Copies README.md and install.sh into doc/, then runs mkdocs with the given args.
# README links point into doc/ from the repo root, so strip that prefix, and
# drop the "Documentation:" line, which links to the site itself.
set -eu

sed -e 's#](doc/#](#g' \
    -e '/^Documentation: https:\/\/tools\.binaryc\.at\/watchcat/d' \
    README.md > doc/index.md
cp install.sh doc/install.sh

mkdocs "$@"

# The home page is generated; point its edit button at README.md.
if [ "${1:-}" = build ] && [ -f site/index.html ]; then
  sed -i 's#/edit/main/doc/index\.md#/edit/main/README.md#g' site/index.html
fi
