//go:build !gnui_release

package app

// StartBridge starts the agent bridge on the socket at path for an offscreen
// app, as GNUI_INSPECT does for a running one, and returns the function that
// stops it. PumpBridge serves what it queued; a test calls both from the
// thread that owns the Offscreen.
func (o *Offscreen) StartBridge(path string) (stop func(), err error) {
	in, err := o.App.startInspector(path)
	if err != nil {
		return nil, err
	}
	return in.stop, nil
}

// PumpBridge runs the safe point of the offscreen app: the bridge's queued
// requests are taken here, as the loop of a live app does between frames.
func (o *Offscreen) PumpBridge() { o.App.safePoint() }
