# fridashot

Capture an iOS app's screen over Frida — **no Developer Disk Image, no CoreDevice tunnel, no `idevicescreenshot`**. Works on jailbroken iOS (incl. **iOS 17**) with `frida-server` running.

It renders the app's key `UIWindow` to a PNG in-process (`UIGraphicsBeginImageContextWithOptions` + `CALayer.renderInContext` + `UIImagePNGRepresentation`) and returns the bytes over the Frida channel. Useful when the DDI isn't mounted and the CoreDevice screenshot service hangs or is unavailable.

## Usage

```bash
fridashot -n "Twitter" -o shot.png          # attach to a running app by display/process name
fridashot -p 1234         -o shot.png           # attach by PID
fridashot -b com.example.app -delay 9 -o shot.png   # spawn fresh, wait 9s for UI, capture, then kill
fridashot -b com.example.app -keep -o shot.png  # ...and leave it running
fridashot -device <id> -n "App" -o shot.png     # target a specific device
```

Output is a full-resolution PNG (e.g. 1536×2048 on iPad, scale handled by passing `0.0` to the context).

## Why this exists (two problems it works around)

1. **Frida 17 removed `Module.findExportByName`.** Most older screenshot snippets use it to resolve the `UIGraphics*` C functions and silently fail with `TypeError: not a function`. fridashot uses **`Module.getGlobalExportByName`** (falling back to `Process.getModuleByName('UIKitCore').getExportByName`).
2. **frida-go omits the args array on no-arg RPC calls.** `script.ExportsCall("fn")` with no arguments sends a malformed RPC (`args` is `undefined`), so the export runs as `fn(...undefined)` → `cannot read property 'Symbol.iterator' of undefined`, and the call silently no-ops. fridashot always passes a **dummy argument** (`ExportsCall("shoot", 0)`), which the JS exports ignore.

It also avoids returning a Promise from the RPC (frida-go mishandles that): capture runs on the main queue via a `shoot`/`poll` pair, and the PNG comes back base64-encoded in the poll result.

## Build

Needs the **frida-core devkit** (cgo) — `frida-core.h` + `libfrida-core.a` — placed in `./devkit/`.

```bash
# 1. Download the devkit matching your host OS/arch from https://github.com/frida/frida/releases
#    e.g. frida-core-devkit-17.2.11-macos-arm64.tar.xz
mkdir -p devkit && tar -xf frida-core-devkit-*.tar.xz -C devkit

# 2. Build
./build.sh
# (equivalently: CGO_CFLAGS="-I$PWD/devkit -w" CGO_LDFLAGS="-L$PWD/devkit" go build -o fridashot .)
```

The `frida_tools` ObjC bridge (`objc_bridge.js`) is embedded at build time so the device script has `ObjC` without the Frida CLI.

## Notes

- Requires `frida-server` running on the device (jailbroken), reachable over USB (`frida-ps -U`).
- `devkit/`, the `fridashot` binary, and `*.png` are git-ignored (the devkit is ~200 MB).
