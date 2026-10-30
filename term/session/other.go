//go:build !darwin && !linux

package session

import (
	"errors"
	"io"
	"time"
)

// ErrRunning is returned by Serve when the directory's daemon is running.
var ErrRunning = errors.New("session: a daemon is already running there")

// ServeOptions tunes a daemon.
type ServeOptions struct {
	Config
	IdleExit time.Duration
	Stop     <-chan struct{}
}

func NewLocal(Config) Service                                       { return unsupported{} }
func Dial(string) (Service, error)                                  { return nil, ErrUnsupported }
func DialOrLaunch(string, []string, time.Duration) (Service, error) { return nil, ErrUnsupported }
func Serve(string, ServeOptions) error                              { return ErrUnsupported }
func RunDaemon(string, ServeOptions) error                          { return ErrUnsupported }
func DefaultDir(string) string                                      { return "" }

type unsupported struct{}

func (unsupported) Create(Options) (Info, error)                       { return Info{}, ErrUnsupported }
func (unsupported) List() ([]Info, error)                              { return nil, ErrUnsupported }
func (unsupported) Attach(string, io.Writer, Size) (Attachment, error) { return nil, ErrUnsupported }
func (unsupported) Kill(string) error                                  { return ErrUnsupported }
func (unsupported) Shutdown() error                                    { return nil }
func (unsupported) Close() error                                       { return nil }
