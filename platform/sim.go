package platform

// SimAX is the native bridge's tree/diff engine without native accessibility
// objects. It is for headless simulations; all calls belong to the UI thread.
type SimAX struct {
	core      axCore
	src       AXSource
	lastQuery int64
}

// NewSimAX attaches a simulated assistive client to src.
func NewSimAX(src AXSource) *SimAX { return &SimAX{src: src} }

func expireAX(active *bool, last, now int64, src AXSource) {
	if *active && now-last >= 30e9 {
		*active = false
		SetAXSourceActive(src, false)
	}
}

// Active checks the same 30-second expiry as the native bridge, without a wake.
func (s *SimAX) Active() bool {
	expireAX(&s.core.active, s.lastQuery, Now(), s.src)
	return s.core.active
}

// Query activates the client and returns its last published tree.
func (s *SimAX) Query() *AXTree {
	s.Active()
	s.lastQuery = Now()
	if !s.core.active {
		SetAXSourceActive(s.src, true)
		s.core.begin(s.src.AXExport)
	}
	return s.core.tree()
}

// Sync publishes a finished frame only while the client is active.
func (s *SimAX) Sync() {
	if s.Active() {
		s.core.sync(s.src.AXExport)
	}
}
