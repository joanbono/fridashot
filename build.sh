#!/bin/bash
# Build fridashot. Requires the frida-core devkit in ./devkit (frida-core.h + libfrida-core.a).
# Download: https://github.com/frida/frida/releases  (frida-core-devkit-<ver>-<os>-<arch>.tar.xz)
set -e
cd "$(dirname "$0")"
if [ ! -f devkit/frida-core.h ]; then echo "Missing devkit/. See README."; exit 1; fi
export CGO_CFLAGS="-I$PWD/devkit -w" CGO_LDFLAGS="-L$PWD/devkit"
go build -o fridashot .
echo "built: $(pwd)/fridashot"
