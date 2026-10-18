//go:build linux

package platform

import (
	"slices"
	"syscall"
	"unsafe"
)

// Wayland clipboard: wl_data_device_manager on the seat.
//
// Opcodes (wayland.xml):
//
//	wl_data_device_manager: create_data_source 0, get_data_device 1
//	wl_data_device:  requests start_drag 0, set_selection 1, release 2 (v2);
//	                 events data_offer 0, enter 1, leave 2, motion 3, drop 4, selection 5
//	wl_data_source:  requests offer 0, destroy 1; events target 0, send 1, cancelled 2
//	wl_data_offer:   requests accept 0, receive 1, destroy 2; events offer 0

var (
	mimeUTF8Plain  = cstr("text/plain;charset=utf-8")
	mimeUTF8String = cstr("UTF8_STRING")
	mimePlain      = cstr("text/plain")

	// wlMimes are the text types we offer and accept, in preference order;
	// wlOffer.mimes has bit i set when the offer lists wlMimes[i].
	wlMimes = [...][]byte{mimeUTF8Plain, mimeUTF8String, mimePlain}
)

const maxOffers = 4

// wlOffer is a wl_data_offer proxy and which of wlMimes it lists.
type wlOffer struct {
	proxy uintptr
	mimes uint8
}

// wlInitDataDevice creates the seat's data device once both globals are bound.
func (w *Window) wlInitDataDevice() {
	y := &w.wy
	if y.ddm == 0 || y.seat == 0 || y.dd != 0 {
		return
	}
	y.args[0], y.args[1] = 0, uint64(y.seat)
	y.dd = w.req(y.ddm, 1, wlc.dataDevice, y.ddmV, 0)
	listen(y.dd)
	y.sel = -1
}

// ---- events ----

func (w *Window) onDataDevice(op uint32, a unsafe.Pointer) {
	y := &w.wy
	switch op {
	case 0: // data_offer(id): a new offer; its mime types follow
		p := argP(a, 0)
		listen(p)
		slot := -1
		for i := range y.offers {
			if y.offers[i].proxy == 0 {
				slot = i
				break
			}
		}
		if slot < 0 { // evict the oldest that is not the selection
			for i := range y.offers {
				if i != y.sel {
					slot = i
					break
				}
			}
			w.release(y.offers[slot].proxy, 2, 1) // wl_data_offer.destroy
		}
		y.offers[slot] = wlOffer{proxy: p}
	case 5: // selection(id): the clipboard changed; id may be null
		p := argP(a, 0)
		y.sel = -1
		for i := range y.offers {
			o := &y.offers[i]
			if o.proxy == 0 {
				continue
			}
			if o.proxy == p {
				y.sel = i
				continue
			}
			w.release(o.proxy, 2, 1)
			*o = wlOffer{}
		}
	}
}

// onOffer handles wl_data_offer.offer(mime); it reports whether proxy is one
// of our offers.
func (w *Window) onOffer(proxy uintptr, op uint32, a unsafe.Pointer) bool {
	y := &w.wy
	for i := range y.offers {
		o := &y.offers[i]
		if o.proxy != proxy {
			continue
		}
		if op == 0 { // offer(mime_type)
			m := argP(a, 0)
			for k, name := range wlMimes {
				if cstrIs(m, string(name[:len(name)-1])) {
					o.mimes |= 1 << k
				}
			}
		}
		return true
	}
	return false
}

func (w *Window) onDataSource(op uint32, a unsafe.Pointer) {
	y := &w.wy
	c := &w.clip
	switch op {
	case 1: // send(mime_type, fd)
		fd := int(int32(argU(a, 1)))
		m := argP(a, 0)
		ok := false
		for _, name := range wlMimes {
			if cstrIs(m, string(name[:len(name)-1])) {
				ok = true
			}
		}
		if !ok || !c.owned {
			syscall.Close(fd)
			return
		}
		w.wlStartSend(fd)
	case 2: // cancelled: another application took the selection
		c.owned = false
		w.release(y.src, 1, 1) // wl_data_source.destroy
		y.src = 0
	}
}

// ---- copy ----

// wlPublish offers c.own on the data device.
func (w *Window) wlPublish() {
	y := &w.wy
	if y.ddm == 0 || y.dd == 0 {
		return // no data device: the text is only ours
	}
	old := y.src
	y.args[0] = 0
	y.src = w.req(y.ddm, 0, wlc.dataSource, y.ddmV, 0)
	listen(y.src)
	v := version(y.src)
	for _, m := range wlMimes {
		y.args[0] = uint64(uintptr(unsafe.Pointer(&m[0])))
		w.req(y.src, 0, 0, v, 0) // offer(mime_type)
	}
	y.args[0], y.args[1] = uint64(y.src), uint64(y.serial)
	w.req(y.dd, 1, 0, y.ddmV, 0) // set_selection(source, serial)
	if old != 0 {
		w.release(old, 1, 1)
	}
	fr.Call1(wlf.flush, y.display)
}

// wlStartSend writes the clipboard text to fd (ours to close) without
// blocking: what a full pipe does not take is written from the poll set.
func (w *Window) wlStartSend(fd int) {
	c := &w.clip
	syscall.SetNonblock(fd, true)
	data := c.own
	off := 0
	for off < len(data) {
		n, err := syscall.Write(fd, data[off:])
		if n > 0 {
			off += n
		}
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			if err == syscall.EAGAIN && c.nsend < maxSends {
				c.sends[c.nsend] = clipSend{fd: int32(fd), data: data, off: off, deadline: nanotime() + clipTimeout}
				c.nsend++
				return
			}
			break // a closed pipe, or too many writers
		}
	}
	syscall.Close(fd)
}

// wlWriteSend continues send i.
func (w *Window) wlWriteSend(i int) {
	c := &w.clip
	s := &c.sends[i]
	for s.off < len(s.data) {
		n, err := syscall.Write(int(s.fd), s.data[s.off:])
		if n > 0 {
			s.off += n
			s.deadline = nanotime() + clipTimeout
		}
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN {
			return
		}
		if err != nil {
			break
		}
	}
	w.wlDropSend(i)
}

// wlDropSend closes send i and removes it.
func (w *Window) wlDropSend(i int) {
	c := &w.clip
	syscall.Close(int(c.sends[i].fd))
	c.nsend--
	c.sends[i] = c.sends[c.nsend]
	c.sends[c.nsend] = clipSend{}
}

func (w *Window) wlSendDeadline() int64 {
	c := &w.clip
	d := int64(-1)
	for i := 0; i < c.nsend; i++ {
		if t := c.sends[i].deadline; d < 0 || t < d {
			d = t
		}
	}
	return d
}

// ---- paste ----

// wlBeginPaste asks the selection offer for its text and starts receiving it.
// It reports false when there is no usable offer.
func (w *Window) wlBeginPaste() bool {
	y := &w.wy
	c := &w.clip
	if y.sel < 0 {
		return false
	}
	o := &y.offers[y.sel]
	k := -1
	for i := range wlMimes {
		if o.mimes&(1<<i) != 0 {
			k = i
			break
		}
	}
	if k < 0 || o.proxy == 0 {
		return false
	}
	// The write end goes to the other application and must stay blocking: it
	// shares its file description with the copy the compositor passes on.
	if err := syscall.Pipe2(c.pipe[:], syscall.O_CLOEXEC); err != nil {
		return false
	}
	syscall.SetNonblock(c.pipe[0], true)
	y.args[0], y.args[1] = uint64(uintptr(unsafe.Pointer(&wlMimes[k][0]))), uint64(uint32(c.pipe[1]))
	w.req(o.proxy, 1, 0, version(o.proxy), 0) // receive(mime_type, fd)
	syscall.Close(c.pipe[1])                  // libwayland sent its own dup
	c.rfd = int32(c.pipe[0])
	c.buf = c.buf[:0]
	fr.Call1(wlf.flush, y.display)
	return true
}

// wlCancelPaste closes the paste pipe.
func (w *Window) wlCancelPaste() {
	if c := &w.clip; c.rfd >= 0 {
		syscall.Close(int(c.rfd))
		c.rfd = -1
	}
}

// wlReadPaste reads what the owner has written so far.
func (w *Window) wlReadPaste() {
	c := &w.clip
	for c.rfd >= 0 {
		if len(c.buf) == cap(c.buf) {
			c.buf = slices.Grow(c.buf, clipChunk)
		}
		room := c.buf[len(c.buf):cap(c.buf)]
		if limit := MaxPaste + 1 - len(c.buf); len(room) > limit {
			room = room[:limit]
		}
		n, err := syscall.Read(int(c.rfd), room)
		switch {
		case err == syscall.EINTR:
			continue
		case err == syscall.EAGAIN:
			return
		case err != nil:
			w.clipFail()
			return
		case n == 0: // EOF: the owner is done
			w.wlCancelPaste()
			w.clipFinish()
			return
		}
		c.buf = c.buf[:len(c.buf)+n]
		c.deadline = nanotime() + clipTimeout
		if len(c.buf) > MaxPaste {
			w.clipFail()
			return
		}
	}
}

// ---- poll set ----

func (w *Window) wlPollFDs() {
	c := &w.clip
	c.pollRead = -1
	if c.rfd >= 0 {
		w.pfd[w.npfd] = pollfd{fd: c.rfd, events: pollIn}
		c.pollRead = w.npfd
		w.npfd++
	}
	for i := 0; i < c.nsend; i++ {
		w.pfd[w.npfd] = pollfd{fd: c.sends[i].fd, events: pollOut}
		c.pollSend[i] = w.npfd
		w.npfd++
	}
}

// wlPollDone moves the transfers that are ready or timed out along; it runs
// right after the poll.
func (w *Window) wlPollDone() {
	c := &w.clip
	now := nanotime()
	if c.pollRead >= 0 && w.pfd[c.pollRead].revents&(pollIn|pollHup|pollErr) != 0 {
		w.wlReadPaste()
	}
	// Backwards: dropping a send moves the last one into its place, and that
	// one was handled already.
	for i := c.nsend - 1; i >= 0; i-- {
		if p := c.pollSend[i]; p >= 0 && w.pfd[p].revents != 0 {
			w.wlWriteSend(i)
		} else if now >= c.sends[i].deadline {
			w.wlDropSend(i)
		}
	}
	for i := range c.pollSend {
		c.pollSend[i] = -1
	}
	w.clipTick(now)
}

// wlClipClose releases the clipboard's descriptors and proxies.
func (w *Window) wlClipClose() {
	y := &w.wy
	c := &w.clip
	w.wlCancelPaste()
	for c.nsend > 0 {
		w.wlDropSend(c.nsend - 1)
	}
	for i := range y.offers {
		w.release(y.offers[i].proxy, 2, 1)
		y.offers[i] = wlOffer{}
	}
	w.release(y.src, 1, 1)
	y.src = 0
	w.release(y.dd, 2, 2) // wl_data_device.release (v2)
	y.dd = 0
	w.release(y.ddm, 0, 99) // no destructor
	y.ddm = 0
}
