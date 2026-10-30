//go:build darwin

package webview

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/core"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// macOS (H1): a WKWebView subclass of ours (GNUIWebView) as a hosted view
// (platform.HostView, focusable), with one GNUIWebViewDelegate per view as its
// navigation delegate, UI delegate, the delegate of its downloads and the
// observer of its properties.
//
//   - WebKit is loaded by the first New (dlopen of WebKit.framework), never
//     at startup.
//   - The session: a fresh nonPersistentDataStore per view, or for a profile
//     dataStoreForIdentifier: (macOS 14) with a UUID derived from the
//     profile's name, which WebKit keeps in the app's own data folder, apart
//     from the app's default store.
//   - The policy is applied in the delegate, in the call: the scheme
//     allow-list in decidePolicyForNavigationAction:, new windows in
//     createWebViewWithConfiguration: (loaded in place with loadRequest:, or
//     refused), camera and microphone in
//     requestMediaCapturePermissionForOrigin:, downloads by the navigation's
//     shouldPerformDownload, a response the view cannot show or one sent as an
//     attachment. Every decision handler is called before the delegate
//     method returns, so no block is kept (D36).
//   - Title, URL, estimatedProgress, loading, canGoBack and canGoForward are
//     observed with KVO (main thread) into the view's State; a download's
//     NSProgress is observed for DownloadProgress, on whatever thread
//     WebKit updates it, by a handler that only posts (throttled to 20 a
//     second).
//   - Events go to the app object through one ring (platform.SourceWebView),
//     with the view's ID as the event's ID.

const (
	navCancel   = 0 // WKNavigationActionPolicy / WKNavigationResponsePolicy
	navAllow    = 1
	navDownload = 2

	permGrant = 1 // WKPermissionDecision
	permDeny  = 2

	progressEvery = int64(50 * time.Millisecond)
)

// KVO contexts: the property, or a download's progress (ctxDownload | view
// << 24 | download).
const (
	ctxTitle = iota + 1
	ctxURL
	ctxProgress0 // estimatedProgress
	ctxLoading
	ctxBack
	ctxForward
	ctxDownload = 1 << 62
)

var kvoPaths = [...]string{ctxTitle: "title", ctxURL: "URL", ctxProgress0: "estimatedProgress", ctxLoading: "loading", ctxBack: "canGoBack", ctxForward: "canGoForward"}

var wk struct {
	loaded  bool
	err     error
	viewDef *objc.ClassDef
	viewCls objc.ID
	delDef  *objc.ClassDef
	delCls  objc.ID
	ring    *platform.Ring
	views   map[uint32]*view
	next    uint32
	nextDL  uint32
	paths   [len(kvoPaths)]objc.ID // retained key paths
	stores  map[string]objc.ID     // a profile's data store (+1), kept for the process (dataStore)
	lastDL  atomic.Int64           // the time of the last DownloadProgress posted, ns
	// loadTime is how long loading WebKit and registering the classes took
	// (the first New), for the gate's report.
	loadTime time.Duration
}

var (
	selAlloc              = objc.Sel("alloc")
	selInit               = objc.Sel("init")
	selInitFrameConfig    = objc.Sel("initWithFrame:configuration:")
	selSetDataStore       = objc.Sel("setWebsiteDataStore:")
	selNonPersistent      = objc.Sel("nonPersistentDataStore")
	selStoreForID         = objc.Sel("dataStoreForIdentifier:")
	selInitUUIDBytes      = objc.Sel("initWithUUIDBytes:")
	selPreferences        = objc.Sel("preferences")
	selSetJSOpenWindows   = objc.Sel("setJavaScriptCanOpenWindowsAutomatically:")
	selSetAppName         = objc.Sel("setApplicationNameForUserAgent:")
	selSetNavDelegate     = objc.Sel("setNavigationDelegate:")
	selSetUIDelegate      = objc.Sel("setUIDelegate:")
	selSetInspectable     = objc.Sel("setInspectable:")
	selSetGestures        = objc.Sel("setAllowsBackForwardNavigationGestures:")
	selSetWantsLayer      = objc.Sel("setWantsLayer:")
	selLayer              = objc.Sel("layer")
	selSetCornerRadius    = objc.Sel("setCornerRadius:")
	selSetMasksToBounds   = objc.Sel("setMasksToBounds:")
	selRespondsTo         = objc.Sel("respondsToSelector:")
	selAddObserver        = objc.Sel("addObserver:forKeyPath:options:context:")
	selRemoveObserver     = objc.Sel("removeObserver:forKeyPath:context:")
	selObserve            = objc.Sel("observeValueForKeyPath:ofObject:change:context:")
	selURLWithString      = objc.Sel("URLWithString:")
	selFileURLWithPath    = objc.Sel("fileURLWithPath:")
	selRequestWithURL     = objc.Sel("requestWithURL:")
	selLoadRequest        = objc.Sel("loadRequest:")
	selReload             = objc.Sel("reload")
	selStopLoading        = objc.Sel("stopLoading")
	selGoBack             = objc.Sel("goBack")
	selGoForward          = objc.Sel("goForward")
	selTitle              = objc.Sel("title")
	selURL                = objc.Sel("URL")
	selAbsoluteString     = objc.Sel("absoluteString")
	selScheme             = objc.Sel("scheme")
	selEstimatedProgress  = objc.Sel("estimatedProgress")
	selIsLoading          = objc.Sel("isLoading")
	selCanGoBack          = objc.Sel("canGoBack")
	selCanGoForward       = objc.Sel("canGoForward")
	selRequest            = objc.Sel("request")
	selShouldDownload     = objc.Sel("shouldPerformDownload")
	selTargetFrame        = objc.Sel("targetFrame")
	selResponse           = objc.Sel("response")
	selCanShowMIME        = objc.Sel("canShowMIMEType")
	selIsForMainFrame     = objc.Sel("isForMainFrame")
	selValueForHeader     = objc.Sel("valueForHTTPHeaderField:")
	selIsKindOfClass      = objc.Sel("isKindOfClass:")
	selSetDelegate        = objc.Sel("setDelegate:")
	selProgress           = objc.Sel("progress")
	selCompletedUnits     = objc.Sel("completedUnitCount")
	selTotalUnits         = objc.Sel("totalUnitCount")
	selOriginalRequest    = objc.Sel("originalRequest")
	selCancelDownload     = objc.Sel("cancel:")
	selRemoveAllItems     = objc.Sel("removeAllItems")
	selWillOpenMenu       = objc.Sel("willOpenMenu:withEvent:")
	selCode               = objc.Sel("code")
	selDomain             = objc.Sel("domain")
	selIsEqualString      = objc.Sel("isEqualToString:")
	selDecideAction       = objc.Sel("webView:decidePolicyForNavigationAction:decisionHandler:")
	selDecideResponse     = objc.Sel("webView:decidePolicyForNavigationResponse:decisionHandler:")
	selDidStart           = objc.Sel("webView:didStartProvisionalNavigation:")
	selDidFinish          = objc.Sel("webView:didFinishNavigation:")
	selDidFail            = objc.Sel("webView:didFailNavigation:withError:")
	selDidFailProv        = objc.Sel("webView:didFailProvisionalNavigation:withError:")
	selProcessDied        = objc.Sel("webViewWebContentProcessDidTerminate:")
	selActionDownload     = objc.Sel("webView:navigationAction:didBecomeDownload:")
	selResponseDownload   = objc.Sel("webView:navigationResponse:didBecomeDownload:")
	selCreateWebView      = objc.Sel("webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:")
	selMediaPermission    = objc.Sel("webView:requestMediaCapturePermissionForOrigin:initiatedByFrame:type:decisionHandler:")
	selDecideDestination  = objc.Sel("download:decideDestinationUsingResponse:suggestedFilename:completionHandler:")
	selDownloadDidFinish  = objc.Sel("downloadDidFinish:")
	selDownloadDidFail    = objc.Sel("download:didFailWithError:resumeData:")
	clsNSURL, clsRequest  objc.ID
	clsHTTPResponse       objc.ID
	strDisposition        objc.ID
	strCompleted          objc.ID
	strNSURLErrorDomain   objc.ID
	strWebKitErrorDomain  objc.ID
	webKitFrameInterrupt  = 102  // WebKitErrorFrameLoadInterruptedByPolicyChange: a download took the navigation
	nsURLErrorCancelled   = -999 // a navigation stopped or replaced
	webKitPath            = "/System/Library/Frameworks/WebKit.framework/WebKit"
	errNoDataStoreForName = fmt.Errorf("%w: named profiles need macOS 14", ErrUnsupported)
)

// load loads WebKit and registers the classes, once (main thread).
func load() error {
	if wk.loaded {
		return wk.err
	}
	wk.loaded = true
	t0 := time.Now()
	if _, err := purego.Dlopen(webKitPath, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		wk.err = fmt.Errorf("%w: %v", ErrUnavailable, err)
		return wk.err
	}
	wkView := objc.Cls("WKWebView")
	if wkView == 0 {
		wk.err = fmt.Errorf("%w: no WKWebView", ErrUnavailable)
		return wk.err
	}
	vd := objc.Define("GNUIWebView", wkView, nil)
	vd.Method(selWillOpenMenu, "v@:@@", willOpenMenu)
	wk.viewCls, wk.viewDef = vd.Register(), vd

	d := objc.Define("GNUIWebViewDelegate", objc.Cls("NSObject"), []string{"WKNavigationDelegate", "WKUIDelegate", "WKDownloadDelegate"})
	d.Method(selDecideAction, "v@:@@@?", decideAction)
	d.Method(selDecideResponse, "v@:@@@?", decideResponse)
	d.Method(selDidStart, "v@:@@", func(c *ffi.CB) { post(c, LoadStarted, 0, 0) })
	d.Method(selDidFinish, "v@:@@", func(c *ffi.CB) { post(c, LoadFinished, 0, 0) })
	d.Method(selDidFail, "v@:@@@", didFail)
	d.Method(selDidFailProv, "v@:@@@", didFail)
	d.Method(selProcessDied, "v@:@", func(c *ffi.CB) { post(c, ProcessFailed, 0, 0) })
	d.Method(selActionDownload, "v@:@@@", becameDownload)
	d.Method(selResponseDownload, "v@:@@@", becameDownload)
	d.Method(selCreateWebView, "@@:@@@@", createWebView)
	d.Method(selMediaPermission, "v@:@@@q@?", mediaPermission)
	d.Method(selDecideDestination, "v@:@@@@?", decideDestination)
	d.Method(selDownloadDidFinish, "v@:@", downloadDidFinish)
	d.Method(selDownloadDidFail, "v@:@@@", downloadDidFail)
	d.Method(selObserve, "v@:@@@^v", observe)
	wk.delCls, wk.delDef = d.Register(), d

	clsNSURL, clsRequest, clsHTTPResponse = objc.Cls("NSURL"), objc.Cls("NSURLRequest"), objc.Cls("NSHTTPURLResponse")
	strDisposition = objc.Retain(objc.Str("Content-Disposition"))
	strCompleted = objc.Retain(objc.Str("completedUnitCount"))
	strNSURLErrorDomain = objc.Retain(objc.Str("NSURLErrorDomain"))
	strWebKitErrorDomain = objc.Retain(objc.Str("WebKitErrorDomain"))
	for i, p := range kvoPaths {
		if p != "" {
			wk.paths[i] = objc.Retain(objc.Str(p))
		}
	}
	wk.ring = platform.NewRing(platform.SourceWebView, 256, 0)
	wk.views = map[uint32]*view{}
	wk.loadTime = time.Since(t0)
	return nil
}

// view is the macOS backend of a View.
type view struct {
	id       uint32
	opt      *Options
	w        *platform.Window
	host     platform.HostID
	wv       objc.ID // GNUIWebView (+1)
	del      objc.ID // GNUIWebViewDelegate (+1)
	st       State
	scratch  []byte
	blockedU string
	dls      []*download
	closed   bool
}

// download is one of a view's downloads.
type download struct {
	Download
	dl       objc.ID // WKDownload (+1)
	progress objc.ID // its NSProgress, observed while running (0: not observed)
	ended    bool
}

func osNew(w *platform.Window, o *Options) (backend, platform.HostID, uint32, error) {
	if err := load(); err != nil {
		return nil, 0, 0, err
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)

	store, err := dataStore(o.Profile)
	if err != nil {
		return nil, 0, 0, err
	}
	cfg := objc.Send(objc.Send(objc.Cls("WKWebViewConfiguration"), selAlloc), selInit)
	defer objc.Release(cfg)
	objc.Send1(cfg, selSetDataStore, uintptr(store))
	objc.Send1(objc.Send(cfg, selPreferences), selSetJSOpenWindows, 1) // window.open reaches createWebView:, where Options.NewWindows decides
	if o.ApplicationName != "" {
		objc.Send1(cfg, selSetAppName, uintptr(objc.Str(o.ApplicationName)))
	}
	alloc := objc.Send(wk.viewCls, selAlloc) // before the call is assembled: Send uses the same frame
	wv := objc.ID(objc.F.Reset().Arg(uintptr(alloc)).Arg(uintptr(selInitFrameConfig)).
		ArgRect(0, 0, 100, 100).Arg(uintptr(cfg)).Call(objc.MsgSend).R0())
	if wv == 0 {
		return nil, 0, 0, fmt.Errorf("%w: WKWebView did not initialise", ErrRuntime)
	}
	wk.next++
	v := &view{id: wk.next, opt: o, w: w, wv: wv}
	wk.viewDef.Index().Set(wv, uintptr(v.id))
	v.del = objc.Send(objc.Send(wk.delCls, selAlloc), selInit)
	wk.delDef.Index().Set(v.del, uintptr(v.id))
	objc.Send1(wv, selSetNavDelegate, uintptr(v.del))
	objc.Send1(wv, selSetUIDelegate, uintptr(v.del))
	objc.Send1(wv, selSetGestures, 1)
	if o.CornerRadius > 0 {
		objc.Send1(wv, selSetWantsLayer, 1)
		layer := objc.Send(wv, selLayer)
		objc.F.Reset().Arg(uintptr(layer)).Arg(uintptr(selSetCornerRadius)).ArgF64(float64(o.CornerRadius)).Call(objc.MsgSend)
		objc.Send1(layer, selSetMasksToBounds, 1)
	}
	if objc.SendBool1(wv, selRespondsTo, uintptr(selSetInspectable)) {
		objc.Send1(wv, selSetInspectable, uintptr(b2u(o.Inspectable)))
	}
	host, err := w.NewHost(platform.HostView, platform.HostOptions{View: uintptr(wv), Focusable: true})
	if err != nil {
		objc.Send1(wv, selSetNavDelegate, 0)
		objc.Send1(wv, selSetUIDelegate, 0)
		objc.Release(v.del)
		objc.Release(wv)
		return nil, 0, 0, err
	}
	v.host = host
	for i, p := range wk.paths {
		if p != 0 {
			objc.F.Reset().Arg(uintptr(wv)).Arg(uintptr(selAddObserver)).Arg(uintptr(v.del)).Arg(uintptr(p)).Arg(0).Arg(uintptr(i)).Call(objc.MsgSend)
		}
	}
	wk.views[v.id] = v
	return v, host, v.id, nil
}

// dataStore is a fresh non-persistent store (profile "", autoreleased) or
// the persistent store of the profile. A profile's store is made once and
// kept for the process: WebKit holds a store only while something uses it,
// and one made again for the identifier after the last view of the profile
// closed may start from what is on disk, without a cookie set a moment
// before (macOS 15: a new view of the profile read no cookie where the
// closed one had set it; the cookie was on disk by the next launch).
func dataStore(profile string) (objc.ID, error) {
	cls := objc.Cls("WKWebsiteDataStore")
	if profile == "" {
		return objc.Send(cls, selNonPersistent), nil
	}
	if s, ok := wk.stores[profile]; ok {
		return s, nil
	}
	if !objc.SendBool1(cls, selRespondsTo, uintptr(selStoreForID)) {
		return 0, errNoDataStoreForName
	}
	u := profileUUID(profile)
	id := objc.Send1(objc.Send(objc.Cls("NSUUID"), selAlloc), selInitUUIDBytes, uintptr(unsafe.Pointer(&u)))
	defer objc.Release(id)
	s := objc.Retain(objc.Send1(cls, selStoreForID, uintptr(id)))
	if s == 0 {
		return 0, fmt.Errorf("%w: no data store for the profile", ErrRuntime)
	}
	if wk.stores == nil {
		wk.stores = map[string]objc.ID{}
	}
	wk.stores[profile] = s
	return s, nil
}

// profileUUID is the identifier of a profile's data store: a version 8
// (name-based, our own hash) UUID of the profile's name, the same in every
// run, so a profile finds its data again.
func profileUUID(name string) [16]byte {
	h := fnv.New128a()
	h.Write([]byte("gonativeui/app/webview profile\x00"))
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil))
	u[6] = u[6]&0x0F | 0x80 // version 8
	u[8] = u[8]&0x3F | 0x80 // RFC 9562 variant
	return u
}

func b2u(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// viewOf is the view of a delegate or a web view (its Index), nil once closed.
func viewOf(self objc.ID, def *objc.ClassDef) *view {
	v := wk.views[uint32(def.Index().Get(self))]
	if v == nil || v.closed {
		return nil
	}
	return v
}

// post queues event kind of the view whose delegate is the callback's self.
func post(c *ffi.CB, kind EventKind, perm Permission, dl uint32) {
	if v := viewOf(objc.ID(c.Int(0)), wk.delDef); v != nil {
		v.post(kind, perm, dl)
	}
}

func (v *view) post(kind EventKind, perm Permission, dl uint32) {
	wk.ring.OfferID(uint64(v.id), int64(kind)|int64(perm)<<8, int64(dl), nil)
}

// allowedURL reports whether URL u's scheme is in the allow-list.
func (v *view) allowedURL(u objc.ID) bool {
	if u == 0 {
		return false
	}
	v.scratch = objc.AppendUTF8(v.scratch[:0], objc.Send(u, selScheme))
	for _, s := range v.opt.Schemes {
		if len(s) == len(v.scratch) && strings.EqualFold(s, unsafe.String(unsafe.SliceData(v.scratch), len(v.scratch))) {
			return true
		}
	}
	return false
}

// block notes u as the last blocked URL and posts kind.
func (v *view) block(u objc.ID, kind EventKind) {
	v.blockedU = string(objc.AppendUTF8(v.scratch[:0], objc.Send(u, selAbsoluteString)))
	v.post(kind, 0, 0)
}

// decideAction is webView:decidePolicyForNavigationAction:decisionHandler:.
func decideAction(c *ffi.CB) {
	action, handler := objc.ID(c.Int(3)), objc.ID(c.Int(4))
	policy := uintptr(navCancel)
	if v := viewOf(objc.ID(c.Int(0)), wk.delDef); v != nil {
		u := objc.Send(objc.Send(action, selRequest), selURL)
		switch {
		case !v.allowedURL(u):
			v.block(u, NavigationBlocked)
		case objc.SendBool1(action, selRespondsTo, uintptr(selShouldDownload)) && objc.SendBool(action, selShouldDownload):
			if v.opt.DownloadDir != "" {
				policy = navDownload
			}
		default:
			policy = navAllow
		}
	}
	objc.CallBlock(c.Frame(), handler, policy)
}

// decideResponse is webView:decidePolicyForNavigationResponse:decisionHandler::
// a main-frame response the view cannot show, or one sent as an attachment,
// is a download (or nothing, without a download folder).
func decideResponse(c *ffi.CB) {
	nr, handler := objc.ID(c.Int(3)), objc.ID(c.Int(4))
	policy := uintptr(navAllow)
	if v := viewOf(objc.ID(c.Int(0)), wk.delDef); v != nil && objc.SendBool(nr, selIsForMainFrame) {
		resp := objc.Send(nr, selResponse)
		download := !objc.SendBool(nr, selCanShowMIME)
		if !download && objc.SendBool1(resp, selIsKindOfClass, uintptr(clsHTTPResponse)) {
			v.scratch = objc.AppendUTF8(v.scratch[:0], objc.Send1(resp, selValueForHeader, uintptr(strDisposition)))
			download = len(v.scratch) >= 10 && strings.EqualFold(string(v.scratch[:10]), "attachment")
		}
		if download {
			policy = navCancel
			if v.opt.DownloadDir != "" {
				policy = navDownload
			}
		}
	}
	objc.CallBlock(c.Frame(), handler, policy)
}

// didFail is both navigation failures: LoadFailed, except for a navigation
// stopped, replaced or turned into a download.
func didFail(c *ffi.CB) {
	err := objc.ID(c.Int(4))
	code := int(objc.Send(err, selCode))
	dom := objc.Send(err, selDomain)
	if code == nsURLErrorCancelled && objc.SendBool1(dom, selIsEqualString, uintptr(strNSURLErrorDomain)) ||
		code == webKitFrameInterrupt && objc.SendBool1(dom, selIsEqualString, uintptr(strWebKitErrorDomain)) {
		return
	}
	post(c, LoadFailed, 0, 0)
}

// createWebView is the UI delegate's request for a new window: loaded in
// this view or refused, never a second view.
func createWebView(c *ffi.CB) {
	c.Ret(0)
	v := viewOf(objc.ID(c.Int(0)), wk.delDef)
	if v == nil {
		return
	}
	req := objc.Send(objc.ID(c.Int(4)), selRequest)
	u := objc.Send(req, selURL)
	switch {
	case v.opt.NewWindows == NewWindowRefuse:
		v.block(u, NewWindowRefused)
	case !v.allowedURL(u):
		v.block(u, NavigationBlocked)
	default:
		objc.Send1(v.wv, selLoadRequest, uintptr(req))
	}
}

// mediaPermission is the camera and microphone request: granted only when
// Options.Allow names what it asks for.
func mediaPermission(c *ffi.CB) {
	typ, handler := c.Int(5), objc.ID(c.Int(6))
	var want Permission
	switch typ { // WKMediaCaptureType
	case 0:
		want = PermCamera
	case 1:
		want = PermMicrophone
	default:
		want = PermCamera | PermMicrophone
	}
	decision := uintptr(permDeny)
	if v := viewOf(objc.ID(c.Int(0)), wk.delDef); v != nil {
		if v.opt.Allow&want == want {
			decision = permGrant
		} else {
			v.post(PermissionDenied, want, 0)
		}
	}
	objc.CallBlock(c.Frame(), handler, decision)
}

// becameDownload is both didBecomeDownload: calls: the download is the view's,
// its delegate the view's delegate, its progress observed.
func becameDownload(c *ffi.CB) {
	v := viewOf(objc.ID(c.Int(0)), wk.delDef)
	dl := objc.ID(c.Int(4))
	if v == nil || dl == 0 {
		return
	}
	wk.nextDL++
	d := &download{Download: Download{ID: wk.nextDL, Total: -1, State: DownloadRunning}, dl: objc.Retain(dl)}
	d.URL = string(objc.AppendUTF8(nil, objc.Send(objc.Send(objc.Send(dl, selOriginalRequest), selURL), selAbsoluteString)))
	objc.Send1(dl, selSetDelegate, uintptr(v.del))
	if p := objc.Send(dl, selProgress); p != 0 {
		d.progress = p
		ctx := uintptr(ctxDownload | uint64(v.id)<<24 | uint64(d.ID)&0xFFFFFF)
		objc.F.Reset().Arg(uintptr(p)).Arg(uintptr(selAddObserver)).Arg(uintptr(v.del)).Arg(uintptr(strCompleted)).Arg(0).Arg(ctx).Call(objc.MsgSend)
	}
	v.dls = append(v.dls, d)
	v.post(DownloadStarted, 0, d.ID)
}

// taken reports whether one of the view's downloads is written to p.
func (v *view) taken(p string) bool {
	for _, d := range v.dls {
		if d.Path == p {
			return true
		}
	}
	return false
}

// downloadOf is the view and record of WKDownload dl, for the download
// delegate's calls.
func downloadOf(self, dl objc.ID) (*view, *download) {
	v := viewOf(self, wk.delDef)
	if v == nil {
		return nil, nil
	}
	for _, d := range v.dls {
		if d.dl == dl {
			return v, d
		}
	}
	return v, nil
}

// decideDestination puts the download in Options.DownloadDir under the
// server's file name, made unique.
func decideDestination(c *ffi.CB) {
	self, dl, name, handler := objc.ID(c.Int(0)), objc.ID(c.Int(2)), objc.ID(c.Int(4)), objc.ID(c.Int(5))
	v, d := downloadOf(self, dl)
	var dest objc.ID
	if v != nil && d != nil && v.opt.DownloadDir != "" {
		if path, err := uniquePath(v.opt.DownloadDir, string(objc.AppendUTF8(nil, name)), v.taken); err == nil {
			d.Path = path
			dest = objc.Send1(clsNSURL, selFileURLWithPath, uintptr(objc.Str(path)))
		}
	}
	objc.CallBlock(c.Frame(), handler, uintptr(dest)) // nil cancels the download
}

func downloadDidFinish(c *ffi.CB) {
	if v, d := downloadOf(objc.ID(c.Int(0)), objc.ID(c.Int(2))); d != nil {
		if fi, err := os.Stat(d.Path); err == nil {
			d.Received = fi.Size()
		}
		v.endDownload(d, DownloadDone)
	}
}

func downloadDidFail(c *ffi.CB) {
	if v, d := downloadOf(objc.ID(c.Int(0)), objc.ID(c.Int(2))); d != nil {
		v.endDownload(d, DownloadFailed)
	}
}

// endDownload ends download d (once): it stops observing its progress, keeps
// its last counts, removes a failed download's file, and posts DownloadEnded.
func (v *view) endDownload(d *download, s DownloadState) {
	if d.ended {
		return
	}
	d.ended = true
	if d.progress != 0 {
		if s != DownloadDone || d.Received == 0 {
			d.Received = int64(objc.Send(d.progress, selCompletedUnits))
		}
		d.Total = int64(objc.Send(d.progress, selTotalUnits))
		ctx := uintptr(ctxDownload | uint64(v.id)<<24 | uint64(d.ID)&0xFFFFFF)
		objc.F.Reset().Arg(uintptr(d.progress)).Arg(uintptr(selRemoveObserver)).Arg(uintptr(v.del)).Arg(uintptr(strCompleted)).Arg(ctx).Call(objc.MsgSend)
		d.progress = 0
	}
	if d.Total < 0 && s == DownloadDone {
		d.Total = d.Received
	}
	if s == DownloadFailed && d.Path != "" {
		os.Remove(d.Path)
	}
	d.State = s
	objc.Send1(d.dl, selSetDelegate, 0)
	v.post(DownloadEnded, 0, d.ID)
}

// observe is KVO's observeValueForKeyPath:ofObject:change:context:. The
// view's properties change on the main thread; a download's progress may
// change on another, so that branch only posts.
func observe(c *ffi.CB) {
	ctx := uint64(c.Int(5))
	if ctx&ctxDownload != 0 {
		now := core.Now()
		last := wk.lastDL.Load()
		if now-last >= progressEvery && wk.lastDL.CompareAndSwap(last, now) {
			wk.ring.OfferID(ctx>>24&0xFFFFFFFF, int64(DownloadProgress), int64(ctx&0xFFFFFF), nil)
		}
		return
	}
	v := viewOf(objc.ID(c.Int(0)), wk.delDef)
	if v == nil {
		return
	}
	wv := v.wv
	switch ctx {
	case ctxTitle:
		if v.setString(&v.st.Title, objc.Send(wv, selTitle)) {
			v.post(TitleChanged, 0, 0)
		}
	case ctxURL:
		if v.setString(&v.st.URL, objc.Send(objc.Send(wv, selURL), selAbsoluteString)) {
			v.post(URLChanged, 0, 0)
		}
	case ctxProgress0:
		if p := float32(objc.SendF64(wv, selEstimatedProgress)); p != v.st.Progress {
			v.st.Progress = p
			v.post(Progress, 0, 0)
		}
	case ctxLoading:
		v.st.Loading = objc.SendBool(wv, selIsLoading)
	case ctxBack, ctxForward:
		b, f := objc.SendBool(wv, selCanGoBack), objc.SendBool(wv, selCanGoForward)
		if b != v.st.CanGoBack || f != v.st.CanGoForward {
			v.st.CanGoBack, v.st.CanGoForward = b, f
			v.post(URLChanged, 0, 0)
		}
	}
}

// setString sets *dst to NSString s's text when it differs (a new string:
// one handed out by State stays valid), and reports whether it did.
func (v *view) setString(dst *string, s objc.ID) bool {
	v.scratch = objc.AppendUTF8(v.scratch[:0], s)
	if string(v.scratch) == *dst {
		return false
	}
	*dst = string(v.scratch)
	return true
}

// willOpenMenu is GNUIWebView's willOpenMenu:withEvent:: with
// Options.NoContextMenu the menu is emptied, and an empty menu does not open.
func willOpenMenu(c *ffi.CB) {
	self, menu, ev := objc.ID(c.Int(0)), objc.ID(c.Int(2)), objc.ID(c.Int(3))
	if v := viewOf(self, wk.viewDef); v != nil && v.opt.NoContextMenu {
		objc.Send(menu, selRemoveAllItems)
		return
	}
	objc.SuperFrame(c.Frame(), wk.viewDef.Superclass(), self, selWillOpenMenu).Arg(uintptr(menu)).Arg(uintptr(ev)).Call(objc.MsgSendSuper)
}

func (v *view) navigate(u string) error {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	url := objc.Send1(clsNSURL, selURLWithString, uintptr(objc.Str(u)))
	if url == 0 {
		return fmt.Errorf("%w: %q", ErrBadURL, u)
	}
	objc.Send1(v.wv, selLoadRequest, uintptr(objc.Send1(clsRequest, selRequestWithURL, uintptr(url))))
	return nil
}

func (v *view) reload()      { objc.Send(v.wv, selReload) }
func (v *view) stop()        { objc.Send(v.wv, selStopLoading) }
func (v *view) back()        { objc.Send(v.wv, selGoBack) }
func (v *view) forward()     { objc.Send(v.wv, selGoForward) }
func (v *view) state() State { return v.st }

func (v *view) blocked() string { return v.blockedU }

func (v *view) download(id uint32) (Download, bool) {
	for _, d := range v.dls {
		if d.ID == id {
			out := d.Download
			if !d.ended && d.progress != 0 {
				out.Received = int64(objc.Send(d.progress, selCompletedUnits))
				out.Total = int64(objc.Send(d.progress, selTotalUnits))
			}
			return out, true
		}
	}
	return Download{}, false
}

func (v *view) cancelDownload(id uint32) {
	for _, d := range v.dls {
		if d.ID == id && !d.ended {
			objc.Send1(d.dl, selCancelDownload, 0)
			v.endDownload(d, DownloadFailed)
		}
	}
}

// close stops the view and every running download, removes its observers,
// delegates and host, and releases it: a non-persistent store goes with it.
func (v *view) close() {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	for _, d := range v.dls {
		if !d.ended {
			objc.Send1(d.dl, selCancelDownload, 0)
			v.endDownload(d, DownloadFailed)
		}
		objc.Release(d.dl)
	}
	v.dls = nil
	v.closed = true
	objc.Send(v.wv, selStopLoading)
	for i, p := range wk.paths {
		if p != 0 {
			objc.F.Reset().Arg(uintptr(v.wv)).Arg(uintptr(selRemoveObserver)).Arg(uintptr(v.del)).Arg(uintptr(p)).Arg(uintptr(i)).Call(objc.MsgSend)
		}
	}
	objc.Send1(v.wv, selSetNavDelegate, 0)
	objc.Send1(v.wv, selSetUIDelegate, 0)
	v.w.ReleaseHost(v.host)
	delete(wk.views, v.id)
	objc.Release(v.del)
	objc.Release(v.wv)
}
