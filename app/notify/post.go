package notify

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Posting. Every Post of an ID is a new generation, the ID's latest post
// (entry.post); a Remove, a RemoveThread or an eviction covers the
// generations made so far (entry.cut). A post is sent only while it is its
// ID's latest and not covered. Each native change (sending a note, removing
// notes) is made holding svc.order together with the bookkeeping it follows
// from, so the system gets them in the order the bookkeeping decided them: a
// post another one superseded is never sent after it. svc.order is never
// waited for on a thread of the system's (a completion handler): what needs
// it there runs on a goroutine.
//
// A post decides what it reserves when it is about to be sent: a free slot
// record; with none, after a reconciliation (once, waiting for it), a slot
// freed by evicting the oldest note (EvictOldest), or nothing (posted without
// actions). Everything that can fail (copying the attachment, making the
// system's request) is done before the eviction, and a note replacing one
// with the same ID leaves the replaced note's slot alone until the system
// has taken the new note (added).

var (
	errSuperseded = errors.New("notify: a later Post or a Remove of the note's ID came first; it was not sent")
	errAttachment = errors.New("notify: attachment")
)

// A post on its way to the system.
type post struct {
	n      Note
	id     string
	gen    uint64
	tk     Ticket // the result's
	pi     int    // its pending entry
	want   bool   // it wants a response (UserData or a Category)
	key    uint64 // its response key, once it has one
	att    string // the attachment's copy
	listed bool   // it waited for a reconciliation already
}

// Post posts a note. A note with UserData or a Category reserves a response
// slot (see the package comment); PostResult says whether it got one, or
// that this is decided later (Pending), and the ResultPost that follows
// says it in every case, with the system's acceptance. A note with an
// Attachment is copied and sent from a goroutine of its own, so Post never
// waits for the file. Post fails with ErrBusy when every request ticket is
// waiting for delivery, and with ErrInvalid for UserData over MaxUserData,
// a Category not registered, or a note the system cannot make.
func Post(n Note) (PostResult, error) {
	if _, err := ready(); err != nil {
		return PostResult{}, err
	}
	if len(n.UserData) > MaxUserData {
		return PostResult{}, fmt.Errorf("%w: UserData is %d bytes, more than %d", ErrInvalid, len(n.UserData), MaxUserData)
	}
	if n.Category != "" && !registered(n.Category) {
		return PostResult{}, fmt.Errorf("%w: category %q is not registered", ErrInvalid, n.Category)
	}
	tk, err := slots.requests.Reserve(0)
	if err != nil {
		return PostResult{}, ErrBusy
	}
	pi, ok := takePending()
	if !ok {
		slots.requests.Cancel(tk)
		return PostResult{}, ErrBusy
	}
	id := n.ID
	if id == "" {
		id = "gnui-" + strconv.FormatUint(uint64(svc.nonce), 36) + "-" + strconv.FormatUint(svc.idSeq.Add(1), 36)
	}
	p := &post{n: n, id: id, tk: tk, pi: pi, want: n.Category != "" || len(n.UserData) > 0}
	svc.mu.Lock()
	svc.gen++
	p.gen = svc.gen
	e := entryOf(id)
	e.post, e.thread = p.gen, n.Thread
	e.inflight++
	svc.mu.Unlock()
	later := PostResult{ID: id, Request: tk, Reservation: Pending}
	if n.Attachment != "" {
		p.n.UserData = bytes.Clone(n.UserData)
		spawn(p.stage)
		return later, nil
	}
	res, wait, err := p.submit()
	switch {
	case wait: // p is the reconciliation's now
		return later, nil
	case err == nil:
		return res, nil
	case errors.Is(err, errSuperseded):
		p.fail(err)
		return PostResult{ID: id, Request: tk}, nil
	}
	p.drop()
	slots.requests.Cancel(tk)
	return PostResult{}, err
}

// workers are the goroutines the service started (tests wait for them).
var workers sync.WaitGroup

// spawn runs f on a goroutine of its own.
func spawn(f func()) {
	workers.Add(1)
	go func() {
		defer workers.Done()
		f()
	}()
}

// stageFile copies an attachment where the system may take it (tests
// replace it).
var stageFile = stageAttachment

// stage copies p's attachment, then sends p (its own goroutine).
func (p *post) stage() {
	att, err := stageFile(p.n.Attachment)
	if err != nil {
		p.fail(fmt.Errorf("%w: %v", errAttachment, err))
		return
	}
	p.att = att
	p.resume()
}

// resume sends p, after its attachment was copied or a reconciliation.
func (p *post) resume() {
	if _, wait, err := p.submit(); !wait && err != nil {
		p.fail(err)
	}
}

// alive reports whether p is its ID's latest post and not removed. svc.mu.
func (p *post) alive() bool {
	e := svc.ids[p.id]
	return e != nil && e.post == p.gen && p.gen > e.cut
}

// submit decides what p reserves, makes its request and sends it, unless p
// was superseded (errSuperseded). With every slot taken it first waits for a
// reconciliation (wait: resumed after it), once.
func (p *post) submit() (res PostResult, wait bool, err error) {
	b := svc.b
	svc.mu.Lock()
	if !p.alive() {
		svc.mu.Unlock()
		return res, false, errSuperseded
	}
	var rec Ticket
	evict := false
	if p.want {
		if tk, err := slots.responses.Reserve(0); err == nil {
			rec = tk
		} else if !p.listed {
			p.listed = true
			svc.waiting = append(svc.waiting, p)
			svc.mu.Unlock()
			reconcileSoon()
			return res, true, nil
		} else {
			evict = svc.opts.EvictOldest && victimSlot(p.id) >= 0
		}
		if (rec != 0 || evict) && p.key == 0 {
			p.key = nextKey()
		}
	}
	keyed := p.key != 0 && (rec != 0 || evict)
	svc.mu.Unlock()
	if !keyed && rec != 0 { // the keys are spent
		cancelRecord(rec)
		rec = 0
	}
	req, err := b.prepare(&p.n, p.id, keyIf(keyed, p.key), p.att, p.pi)
	if err != nil {
		cancelRecord(rec)
		return res, false, err
	}

	svc.order.Lock()
	defer svc.order.Unlock()
	svc.mu.Lock()
	if !p.alive() {
		svc.mu.Unlock()
		cancelRecord(rec)
		b.discard(req)
		return res, false, errSuperseded
	}
	var evicted []string
	if keyed && rec == 0 {
		if tk, err := slots.responses.Reserve(0); err == nil {
			rec = tk
		} else {
			rec, evicted = evictFor(p.id)
		}
	}
	if keyed && rec == 0 { // what could be evicted was answered meanwhile: without actions
		keyed = false
		svc.mu.Unlock()
		b.discard(req)
		req, err = b.prepare(&p.n, p.id, 0, p.att, p.pi)
		svc.mu.Lock()
		made := err == nil
		if made && !p.alive() {
			err = errSuperseded
		}
		if err != nil {
			svc.mu.Unlock()
			if made {
				b.discard(req)
			}
			if len(evicted) > 0 {
				b.remove(evicted)
			}
			return res, false, err
		}
	}
	res = PostResult{ID: p.id, Request: p.tk}
	si := -1
	switch {
	case keyed:
		si = takeSlot(rec, p.key, p.id, p.n.Thread, time.Now().UnixNano(), p.gen)
		res.Reservation, res.Response = Reserved, rec
	case p.want:
		res.Reservation = PostedWithoutActions
		slots.withoutActions.Add(1)
	}
	if len(evicted) > 0 {
		res.Evicted = evicted[0]
	}
	svc.ids[p.id].inflight--
	setPending(p.pi, pending{kind: pendPost, ticket: p.tk, id: p.id, gen: p.gen, slot: si, key: p.key, res: res})
	svc.mu.Unlock()
	if len(evicted) > 0 {
		b.remove(evicted)
	}
	b.send(req)
	return res, false, nil
}

func cancelRecord(tk Ticket) {
	if tk != 0 {
		slots.responses.Cancel(tk)
	}
}

func keyIf(ok bool, key uint64) uint64 {
	if ok {
		return key
	}
	return 0
}

// drop ends a post that was not sent.
func (p *post) drop() {
	if p.att != "" {
		os.Remove(p.att)
	}
	svc.mu.Lock()
	if e := svc.ids[p.id]; e != nil {
		e.inflight--
		tidy(p.id)
	}
	svc.mu.Unlock()
	freePending(p.pi)
}

// fail ends a post that was not sent with a failed ResultPost.
func (p *post) fail(err error) {
	p.drop()
	code := CodeInvalid
	switch {
	case errors.Is(err, errSuperseded):
		code = CodeSuperseded
		slots.superseded.Add(1)
	case errors.Is(err, errAttachment):
		code = CodeAttachment
	}
	completePost(p.tk, PostResult{}, code, []byte(err.Error()))
}

// stageAttachment copies the file into the temporary directory: the system
// moves the file it is given into its own store.
func stageAttachment(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dir := filepath.Join(os.TempDir(), "gnui-notify")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst, err := os.CreateTemp(dir, "*"+filepath.Ext(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), dst.Close()
}

// Remove removes notes from Notification Center (and those not shown yet),
// freeing their response slots. A post of one of the IDs made before the
// call and not sent yet (its Attachment still copying) is not sent: its
// ResultPost has CodeSuperseded.
func Remove(ids ...string) error {
	b, err := ready()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	svc.order.Lock()
	defer svc.order.Unlock()
	svc.mu.Lock()
	for _, id := range ids {
		entryOf(id).cut = svc.gen
		releaseID(id, svc.gen)
		tidy(id)
	}
	svc.mu.Unlock()
	b.remove(ids)
	return nil
}

// RemoveThread removes the notes of a thread (Note.Thread) posted so far,
// freeing their response slots: the IDs whose latest post is in the thread
// at once, and every other note of the thread in Notification Center (plain
// notes, earlier runs') once the system has listed them. A note posted after
// the call is kept, even with the ID of one removed.
func RemoveThread(thread string) error {
	b, err := ready()
	if err != nil {
		return err
	}
	pi, ok := takePending()
	if !ok {
		return ErrBusy
	}
	var ids []string
	svc.order.Lock()
	svc.mu.Lock()
	now, cut := time.Now().UnixNano(), svc.gen
	svc.listings++ // the entries stay until the listing arrives
	for id, e := range svc.ids {
		if e.thread == thread && e.post > e.cut {
			e.cut = cut
			releaseID(id, cut)
			ids = append(ids, id)
		}
	}
	if svc.restoring.Load() {
		svc.threadCuts = append(svc.threadCuts, threadCutAt{thread, now})
	}
	setPending(pi, pending{kind: pendThread, thread: thread, before: now, gen: cut})
	svc.mu.Unlock()
	if len(ids) > 0 {
		slices.Sort(ids)
		b.remove(ids)
	}
	svc.order.Unlock()
	if err := b.delivered(pi); err != nil {
		if _, ok := endPending(pi); ok {
			svc.mu.Lock()
			endListing()
			svc.mu.Unlock()
		}
		return err
	}
	return nil
}

// threadCutAt is a RemoveThread made while the launch's restore runs.
type threadCutAt struct {
	thread string
	before int64
}
