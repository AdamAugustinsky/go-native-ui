//go:build darwin || linux

package session

import (
	"io"
)

// NewLocal returns an in-process service: its sessions are this process's
// children and end with it (Close, or the process's exit, hangs them up).
func NewLocal(cfg Config) Service { return &local{m: newManager(cfg)} }

type local struct{ m *manager }

func (l *local) Create(o Options) (Info, error) {
	s, err := l.m.create(o)
	if err != nil {
		return Info{}, err
	}
	return s.info(), nil
}

func (l *local) List() ([]Info, error) { return l.m.list(), nil }

func (l *local) Attach(id string, sink io.Writer, sz Size) (Attachment, error) {
	s, err := l.m.get(id)
	if err != nil {
		return nil, err
	}
	return &localAttachment{l: l, s: s, sub: s.attach(sinkStream{sink}, sz)}, nil
}

func (l *local) Kill(id string) error { return l.m.kill(id) }
func (l *local) Shutdown() error      { l.m.shutdown(); return nil }
func (l *local) Close() error         { return l.Shutdown() }

// sinkStream writes a session's output straight to a view's sink.
type sinkStream struct{ w io.Writer }

func (w sinkStream) replay(p []byte) error {
	if _, err := io.WriteString(w.w, ResetSequence); err != nil {
		return err
	}
	return w.output(p)
}

func (w sinkStream) output(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	_, err := w.w.Write(p)
	return err
}

func (sinkStream) exit(int) {}
func (sinkStream) cancel()  {}

type localAttachment struct {
	l   *local
	s   *session
	sub *subscriber
}

func (a *localAttachment) ID() string                  { return a.s.id }
func (a *localAttachment) Write(p []byte) (int, error) { return a.s.write(p) }
func (a *localAttachment) Resize(sz Size) error        { a.s.resize(sz); return nil }
func (a *localAttachment) Detach() error               { a.s.detach(a.sub); return nil }
func (a *localAttachment) Close() error                { a.l.m.closeView(a.s, a.sub); return nil }
func (a *localAttachment) Done() <-chan struct{}       { return a.sub.done }
func (a *localAttachment) Exit() (int, bool)           { return a.s.exitOf(a.sub) }
