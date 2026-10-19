// fridashot — capture an iOS app's screen over Frida (no Developer Disk Image, no CoreDevice tunnel).
//
// Renders the key UIWindow to a PNG via UIGraphics + CALayer.renderInContext and streams the bytes
// back over the Frida channel. Works on jailbroken iOS with frida-server (incl. iOS 17, where
// `Module.findExportByName` was removed — this uses `Module.getGlobalExportByName`).
//
// Usage:
//   fridashot -n "Caspar Dev" -o shot.png          # attach to a running app by name
//   fridashot -b com.example.app -o shot.png        # spawn fresh, wait, capture
//   fridashot -p 1234 -o shot.png                   # attach by PID
package main

import (
	_ "embed"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/frida/frida-go/frida"
)

//go:embed objc_bridge.js
var objcBridge string

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

// capture script: prepends nothing here — objcBridge defines `bridge`; we alias ObjC = bridge.
const captureJS = `
var ObjC = bridge;
function ex(n){
  try { return Module.getGlobalExportByName(n); }
  catch(e){ try { return Process.getModuleByName('UIKitCore').getExportByName(n); } catch(e2){ return null; } }
}
var __shot = null;
function __capture(){
  try{
    var app = ObjC.classes.UIApplication.sharedApplication();
    var ws = app.windows();
    var win = ws.count() > 0 ? ws.objectAtIndex_(0) : null;
    for (var i=0;i<ws.count();i++){ var w=ws.objectAtIndex_(i); if (w.keyWindow && w.keyWindow()) win=w; }
    if (win == null){ __shot = {err:'no window'}; return; }
    var b = win.bounds(); var width = b[1][0], height = b[1][1];
    var Begin = new NativeFunction(ex('UIGraphicsBeginImageContextWithOptions'), 'void', [['double','double'],'bool','double']);
    var GetCtx = new NativeFunction(ex('UIGraphicsGetCurrentContext'), 'pointer', []);
    var GetImg = new NativeFunction(ex('UIGraphicsGetImageFromCurrentImageContext'), 'pointer', []);
    var End   = new NativeFunction(ex('UIGraphicsEndImageContext'), 'void', []);
    var PNG   = new NativeFunction(ex('UIImagePNGRepresentation'), 'pointer', ['pointer']);
    Begin([width,height], 0, 0.0);
    win.layer().renderInContext_(GetCtx());
    var img = GetImg(); End();
    var data = new ObjC.Object(PNG(img));
    __shot = {ok:true, len:data.length().valueOf(), dim:width+'x'+height, png:data.base64EncodedStringWithOptions_(0).toString()};
  } catch(e){ __shot = {err: ''+e, stack: ''+e.stack}; }
}
rpc.exports.shoot = function(){
  __shot = null;
  try { ObjC.schedule(ObjC.mainQueue, __capture); }
  catch(e){ __capture(); }   // fall back to inline if scheduling is unavailable
  return true;
};
rpc.exports.cap = function(){ __shot = null; __capture(); return __shot; }; // synchronous inline capture
rpc.exports.poll  = function(){ return __shot; };
`

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "fridashot: "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	out := flag.String("o", "shot.png", "output PNG path")
	name := flag.String("n", "", "attach to a running app by process name")
	pid := flag.Int("p", 0, "attach to a running app by PID")
	bundle := flag.String("b", "", "spawn this bundle identifier fresh, then capture")
	delay := flag.Float64("delay", 8.0, "seconds to wait after spawn before capturing (spawn mode)")
	deviceID := flag.String("device", "", "target a specific device by ID (default: first USB device)")
	keep := flag.Bool("keep", false, "leave a spawned app running (default: kill it afterwards)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("fridashot %s\n", version)
		return
	}

	mgr := frida.NewDeviceManager()
	defer mgr.Close()

	var dev *frida.Device
	var err error
	if *deviceID != "" {
		dev, err = frida.DeviceByID(*deviceID)
		if err != nil {
			fail("device %s: %v", *deviceID, err)
		}
	} else {
		dev = frida.USBDevice()
	}
	if dev == nil {
		fail("no USB device found")
	}
	defer dev.Clean()

	var session *frida.Session
	var spawnedPID int
	switch {
	case *bundle != "":
		spawnedPID, err = dev.Spawn(*bundle, nil)
		if err != nil {
			fail("spawn %s: %v", *bundle, err)
		}
		session, err = dev.Attach(spawnedPID, nil)
		if err != nil {
			fail("attach spawned pid %d: %v", spawnedPID, err)
		}
		if err = dev.Resume(spawnedPID); err != nil {
			fail("resume: %v", err)
		}
		time.Sleep(time.Duration(*delay * float64(time.Second)))
	case *pid != 0:
		session, err = dev.Attach(*pid, nil)
		if err != nil {
			fail("attach pid %d: %v", *pid, err)
		}
	case *name != "":
		session, err = dev.Attach(*name, nil)
		if err != nil {
			fail("attach %q: %v", *name, err)
		}
	default:
		fail("specify one of -n <name>, -p <pid>, or -b <bundle>")
	}
	defer session.Detach()

	script, err := session.CreateScript(objcBridge + captureJS)
	if err != nil {
		fail("create script: %v", err)
	}

	script.On("message", func(message string, data []byte) {})
	if err = script.Load(); err != nil {
		fail("load script: %v", err)
	}

	script.ExportsCall("shoot", 0)
	var m map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r := script.ExportsCall("poll", 0)
		if mm, ok := r.(map[string]any); ok {
			m = mm
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if m == nil {
		fail("timed out waiting for image")
	}
	if e, has := m["err"]; has {
		fail("capture error: %v (%v)", e, m["stack"])
	}
	b64, _ := m["png"].(string)
	if b64 == "" {
		fail("no image in result")
	}
	png, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		fail("decode png: %v", err)
	}
	if err = os.WriteFile(*out, png, 0o644); err != nil {
		fail("write %s: %v", *out, err)
	}
	fmt.Printf("saved %s (%d bytes, %v)\n", *out, len(png), m["dim"])

	if *bundle != "" && !*keep {
		_ = dev.Kill(spawnedPID)
	}
}
