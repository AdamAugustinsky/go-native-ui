package main

import (
	"gonativeui/app"
	"gonativeui/app/dock"
	"gonativeui/app/login"
	"gonativeui/app/notify"
	"gonativeui/app/secret"
	"gonativeui/app/tray"
	"gonativeui/menu"
)

// host is what the app asks of the operating system. The real one is system;
// the tests give the app a recorder, so that they never post a notification,
// touch the Dock or write to the Keychain.
type host interface {
	Post(n notify.Note) error
	SetBadge(n int)
	SetStatusTitle(title string)
	StoreToken(token []byte) error
	LoadToken() ([]byte, error)
	SetLogin(on bool) (login.State, error)
}

// system is the real host: the services of package app/*.
type system struct {
	a      *app.App
	status *tray.Item // nil where there is no status area, or before Start made it
	vault  *secret.Store
}

const account = "demo"

// Post posts a notification. It may be called from any goroutine.
func (s *system) Post(n notify.Note) error {
	_, err := notify.Post(n)
	return err
}

// SetBadge shows an unread count on the Dock icon. The Dock is the main
// thread's, so a call from a frame waits for a safe point.
func (s *system) SetBadge(n int) {
	s.a.AtSafePoint(func() { dock.SetBadgeCount(n) })
}

// SetStatusTitle puts text beside the status item's icon, at a safe point too.
func (s *system) SetStatusTitle(title string) {
	s.a.AtSafePoint(func() {
		if s.status != nil {
			s.status.SetTitle(title)
		}
	})
}

// StoreToken and LoadToken use the Keychain (Credential Manager on Windows, the
// Secret Service on Linux). They block: call them from a goroutine.
func (s *system) StoreToken(token []byte) error { return s.vault.Set(account, token) }
func (s *system) LoadToken() ([]byte, error)    { return s.vault.Get(account) }

// SetLogin makes the app start at login, or not. It blocks too.
func (s *system) SetLogin(on bool) (login.State, error) {
	if on {
		return login.Enable()
	}
	return login.Disable()
}

// newStatusItem makes the status item and its menu: a template symbol, a
// tooltip and Quit. The model's commands arrive as app events.
func newStatusItem() (*tray.Item, error) {
	item, err := tray.New(tray.Options{Icon: tray.SymbolIcon("bell.fill"), Tooltip: "Pings", RightClickMenu: true})
	if err != nil {
		return nil, err
	}
	m, err := menu.NewModel(
		[]menu.Command{{ID: cmdOpen, Title: "Open Pings"}, {ID: cmdQuit, Title: "Quit Pings"}},
		[]menu.Menu{{Title: "Pings", Items: []menu.Item{
			{Kind: menu.ItemCommand, Command: cmdOpen},
			{Kind: menu.ItemSeparator},
			{Kind: menu.ItemCommand, Command: cmdQuit},
		}}}, nil)
	if err != nil {
		item.Remove()
		return nil, err
	}
	return item, item.SetMenu(m, 0)
}
