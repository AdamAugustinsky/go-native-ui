package platform

// The system colour panel (phase 6, D7a). On macOS it is the shared
// NSColorPanel: ShowColorPanel puts it on screen with a colour and makes this
// window's app its target; while it is open every change the user makes (the
// panel is continuous) is one EvColor event on the window's queue with Code 0
// and the colour in X, Y, DX and DY as red, green, blue and alpha, in sRGB,
// 0 to 1; closing it (its close button, or CloseColorPanel is not an event)
// is an EvColor with Code 1. SetColorPanelColor moves the panel to a colour the
// app changed (the colour well's own picker, a hex field) without an EvColor
// coming back for it. Elsewhere the calls return ErrUnsupported and
// NativeColorPanel is false: the app uses its own picker.
//
// The calls run on the main thread, between frames or in a frame, never from
// inside an AppKit callback.
