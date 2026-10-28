package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// A toast posted by an unpackaged executable, its events and the history
// read back.

// Interface ids (windows.ui.notifications.h, windows.data.xml.dom.h). The
// three TypedEventHandler ids are the parameterized interface ids the SDK
// header gives for ToastNotification's events. Verify on first CI run.
var (
	iidToastManagerStatics  = guid("50AC103F-D235-4598-BBEF-98FE4D1A3AD4")
	iidToastManagerStatics2 = guid("7AB93C52-0E48-4750-BA9D-1A4113981847")
	iidToastHistory2        = guid("3BC3D253-2F31-4092-9129-8AD5ABF067DA")
	iidToastFactory         = guid("04124B20-82C6-4229-B109-FD9ED4662B53")
	iidXmlDocumentIO        = guid("6CD0E74E-EE65-4489-9EBF-CA43E87BA637")
	iidXmlDocument          = guid("F7F3A506-1E87-42D6-BCFB-B8C809FA5494")
	iidActivatedHandler     = guid("AB54DE2D-97D9-5528-B6AD-105AFE156530") // TypedEventHandler<ToastNotification, IInspectable>
	iidDismissedHandler     = guid("61C2402F-0ED0-5A18-AB69-59F4AA99A368") // TypedEventHandler<ToastNotification, ToastDismissedEventArgs>
	iidFailedHandler        = guid("95E3E803-C969-5E3A-9753-EA2AD22A9A33") // TypedEventHandler<ToastNotification, ToastFailedEventArgs>
)

// Vtable slots, after IInspectable's six.
const (
	wi                            = win32.InspectableFirst
	vtManagerCreateNotifierWithID = wi + 1 // IToastNotificationManagerStatics: CreateToastNotifier 6, …WithId 7
	vtManager2GetHistory          = wi + 0 // IToastNotificationManagerStatics2::get_History
	vtHistoryClearWithID          = wi + 6 // IToastNotificationHistory: RemoveGroup 6 … Clear 11, ClearWithId 12
	vtHistory2GetHistoryWithID    = wi + 1 // IToastNotificationHistory2: GetHistory 6, GetHistoryWithId 7
	vtVectorViewSize              = wi + 1 // IVectorView<T>: GetAt 6, get_Size 7
	vtXmlLoadXML                  = wi + 0 // IXmlDocumentIO::LoadXml
	vtFactoryCreate               = wi + 0 // IToastNotificationFactory::CreateToastNotification
	vtToastAddDismissed           = wi + 3 // IToastNotification: get_Content 6, put_/get_ExpirationTime 7-8, add_Dismissed 9
	vtToastRemoveDismissed        = wi + 4
	vtToastAddActivated           = wi + 5
	vtToastRemoveActivated        = wi + 6
	vtToastAddFailed              = wi + 7
	vtToastRemoveFailed           = wi + 8
	vtNotifierShow                = wi + 0 // IToastNotifier: Show 6, Hide 7, get_Setting 8
	vtNotifierGetSetting          = wi + 2
	vtFailedArgsErrorCode         = wi + 0 // IToastFailedEventArgs::get_ErrorCode
	vtDismissedArgsReason         = wi + 0 // IToastDismissedEventArgs::get_Reason
)

// settingNames are NotificationSetting's values.
var settingNames = []string{"Enabled", "DisabledForApplication", "DisabledForUser", "DisabledByGroupPolicy", "DisabledByManifest"}

// dismissNames are ToastDismissalReason's values.
var dismissNames = []string{"UserCanceled", "ApplicationHidden", "TimedOut"}

type variantResult struct {
	Name           string            `json:"name"`
	Steps          map[string]string `json:"steps"` // HRESULT per step
	Setting        string            `json:"setting,omitempty"`
	ShowHR         string            `json:"showHR,omitempty"`
	HistoryCount   int               `json:"historyCount"`   // -1: not read
	HistoryFirstMs float64           `json:"historyFirstMs"` // after Show, -1 if never
	Activated      int32             `json:"activated"`
	Dismissed      int32             `json:"dismissed"`
	DismissReason  string            `json:"dismissReason,omitempty"`
	Failed         int32             `json:"failed"`
	FailedError    string            `json:"failedError,omitempty"`
	Posted         bool              `json:"posted"` // Show succeeded and the history has it
}

// events are what the delegates saw, per variant (the object's Value).
var events [2]struct {
	activated, dismissed, failed atomic.Int32
	reason, errCode              atomic.Int32
}

var (
	kindsOnce                       sync.Once
	activatedK, dismissedK, failedK *com.Kind
)

func makeKinds() {
	kindsOnce.Do(func() {
		// Invoke(sender, args) is slot 3 of each delegate (IUnknown-based).
		activatedK = com.NewKind("ToastActivated", 4, []win32.GUID{iidActivatedHandler}, []com.Method{{N: 2, Fn: func(o *com.Object, a com.Args) uintptr {
			events[o.Value()].activated.Add(1)
			return 0
		}}})
		dismissedK = com.NewKind("ToastDismissed", 4, []win32.GUID{iidDismissedHandler}, []com.Method{{N: 2, Fn: func(o *com.Object, a com.Args) uintptr {
			e := &events[o.Value()]
			var reason int32
			if a[1] != 0 && win32.HR(cm(a[1], vtDismissedArgsReason, uintptr(unsafe.Pointer(&reason)))) >= 0 {
				e.reason.Store(reason)
			}
			e.dismissed.Add(1)
			return 0
		}}})
		failedK = com.NewKind("ToastFailed", 4, []win32.GUID{iidFailedHandler}, []com.Method{{N: 2, Fn: func(o *com.Object, a com.Args) uintptr {
			e := &events[o.Value()]
			var code int32
			if a[1] != 0 && win32.HR(cm(a[1], vtFailedArgsErrorCode, uintptr(unsafe.Pointer(&code)))) >= 0 {
				e.errCode.Store(code)
			}
			e.failed.Add(1)
			return 0
		}}})
	})
}

const toastXML = `<toast launch="probe-p6:launch" activationType="protocol">
  <visual><binding template="ToastGeneric"><text>probe-p6</text><text>VARIANT: a toast from an unpackaged executable</text></binding></visual>
  <actions>
    <action content="One" activationType="protocol" arguments="probe-p6:action?x=1"/>
    <action content="Two" activationType="protocol" arguments="probe-p6:action?x=2"/>
  </actions>
</toast>`

// postToast posts one toast with the AUMID id, waits wait for its events and
// the history, and reports.
func postToast(v int, name, id string, wait time.Duration) *variantResult {
	makeKinds()
	r := &variantResult{Name: name, Steps: map[string]string{}, HistoryCount: -1, HistoryFirstMs: -1}
	step := func(k string, hr int32) bool { r.Steps[k] = hrText(hr); return hr >= 0 }
	hid, hr := win32.NewHSTRING(id)
	if !step("HSTRING(id)", hr) {
		return r
	}
	defer hid.Delete()

	var mgr, notifier uintptr
	if !step("ToastNotificationManager statics", win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotificationManager", &iidToastManagerStatics, &mgr)) {
		return r
	}
	defer win32.Release(mgr)
	if !step("CreateToastNotifierWithId", win32.HR(cm(mgr, vtManagerCreateNotifierWithID, uintptr(hid), uintptr(unsafe.Pointer(&notifier))))) {
		return r
	}
	defer win32.Release(notifier)
	var setting int32 = -1
	if step("get_Setting", win32.HR(cm(notifier, vtNotifierGetSetting, uintptr(unsafe.Pointer(&setting))))) && setting >= 0 && int(setting) < len(settingNames) {
		r.Setting = settingNames[setting]
	}

	var insp, io, doc uintptr
	if !step("XmlDocument", win32.RoActivateInstance("Windows.Data.Xml.Dom.XmlDocument", &insp)) {
		return r
	}
	defer win32.Release(insp)
	if !step("IXmlDocumentIO", win32.QueryInterface(insp, &iidXmlDocumentIO, &io)) {
		return r
	}
	defer win32.Release(io)
	hx, hr := win32.NewHSTRING(strings.Replace(toastXML, "VARIANT", name, 1))
	if !step("HSTRING(xml)", hr) {
		return r
	}
	hr = win32.HR(cm(io, vtXmlLoadXML, uintptr(hx)))
	hx.Delete()
	if !step("LoadXml", hr) || !step("IXmlDocument", win32.QueryInterface(insp, &iidXmlDocument, &doc)) {
		return r
	}
	defer win32.Release(doc)

	var fac, toast uintptr
	if !step("ToastNotification factory", win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotification", &iidToastFactory, &fac)) {
		return r
	}
	hr = win32.HR(cm(fac, vtFactoryCreate, doc, uintptr(unsafe.Pointer(&toast))))
	win32.Release(fac)
	if !step("CreateToastNotification", hr) {
		return r
	}
	defer win32.Release(toast)

	// The handlers: added, and removed before the toast goes.
	type reg struct {
		k           *com.Kind
		add, remove uintptr
		name        string
	}
	for _, g := range []reg{
		{activatedK, vtToastAddActivated, vtToastRemoveActivated, "Activated"},
		{dismissedK, vtToastAddDismissed, vtToastRemoveDismissed, "Dismissed"},
		{failedK, vtToastAddFailed, vtToastRemoveFailed, "Failed"},
	} {
		o, _, err := g.k.New(uintptr(v))
		if err != nil {
			r.Steps["handler "+g.name] = err.Error()
			continue
		}
		var token int64 // EventRegistrationToken (eventtoken.h)
		if step("add_"+g.name, win32.HR(cm(toast, g.add, o.Ptr(), uintptr(unsafe.Pointer(&token))))) {
			defer func(rm uintptr) { cm(toast, rm, uintptr(token)) }(g.remove)
		}
		o.Release() // the toast holds its own reference now
	}

	hr = win32.HR(cm(notifier, vtNotifierShow, toast))
	r.ShowHR = hrText(hr)
	shown := time.Now()
	end := shown.Add(wait)
	for time.Now().Before(end) {
		pump()
		if n := historyCount(hid, r.Steps); n >= 0 {
			r.HistoryCount = n
			if n > 0 && r.HistoryFirstMs < 0 {
				r.HistoryFirstMs = float64(time.Since(shown).Microseconds()) / 1000
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e := &events[v]
	r.Activated, r.Dismissed, r.Failed = e.activated.Load(), e.dismissed.Load(), e.failed.Load()
	if r.Dismissed > 0 {
		if n := e.reason.Load(); n >= 0 && int(n) < len(dismissNames) {
			r.DismissReason = dismissNames[n]
		}
	}
	if r.Failed > 0 {
		r.FailedError = hrText(e.errCode.Load())
	}
	r.Posted = hr >= 0 && r.HistoryCount > 0
	return r
}

// historyCount is the number of toasts of the AUMID in the action centre,
// -1 when the history cannot be read.
func historyCount(hid win32.HSTRING, steps map[string]string) int {
	var mgr2, hist, hist2, view uintptr
	hr := win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotificationManager", &iidToastManagerStatics2, &mgr2)
	if hr >= 0 {
		hr = win32.HR(cm(mgr2, vtManager2GetHistory, uintptr(unsafe.Pointer(&hist))))
		win32.Release(mgr2)
	}
	if hr >= 0 {
		hr = win32.QueryInterface(hist, &iidToastHistory2, &hist2)
		win32.Release(hist)
	}
	if hr >= 0 {
		hr = win32.HR(cm(hist2, vtHistory2GetHistoryWithID, uintptr(hid), uintptr(unsafe.Pointer(&view))))
		win32.Release(hist2)
	}
	steps["history"] = hrText(hr)
	if hr < 0 {
		return -1
	}
	defer win32.Release(view)
	var n uint32
	if win32.HR(cm(view, vtVectorViewSize, uintptr(unsafe.Pointer(&n)))) < 0 {
		return -1
	}
	return int(n)
}

// clearHistory removes the AUMID's toasts from the action centre.
func clearHistory(id string) int32 {
	hid, hr := win32.NewHSTRING(id)
	if hr < 0 {
		return hr
	}
	defer hid.Delete()
	var mgr2, hist uintptr
	hr = win32.RoGetActivationFactory("Windows.UI.Notifications.ToastNotificationManager", &iidToastManagerStatics2, &mgr2)
	if hr >= 0 {
		hr = win32.HR(cm(mgr2, vtManager2GetHistory, uintptr(unsafe.Pointer(&hist))))
		win32.Release(mgr2)
	}
	if hr >= 0 {
		hr = win32.HR(cm(hist, vtHistoryClearWithID, uintptr(hid)))
		win32.Release(hist)
	}
	return hr
}
