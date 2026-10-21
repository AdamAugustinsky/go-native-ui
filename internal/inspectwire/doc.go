// Package inspectwire is the wire of the agent bridge (phase 6, T4; DECISIONS
// D47): the JSON messages, the Unix socket that carries them and the checks
// that make the socket safe to leave in a user's directory. It knows nothing
// of windows or widgets: package app serves the requests, cmd/gnui sends them.
//
// # Protocol
//
// A connection carries one request per line and one response per line, in
// order. Both are JSON objects. A request is at most MaxLine bytes including
// its newline; a longer one is answered with the error code ErrTooLarge and
// the connection is closed, since the rest of the line cannot be told from
// the next request.
//
//	{"id":"1","op":"windows"}
//	{"id":"2","op":"tree","window":1,"role":"Button"}
//	{"id":"3","op":"do","action":"click","role":"Button","name":"Add"}
//	{"id":"4","op":"do","steps":[{"action":"focus","role":"TextField","name":"New note"},{"action":"type","text":"milk"},{"action":"key","key":"enter"}]}
//	{"id":"5","op":"shot","path":"/tmp/out.png"}
//
// Every response has "ok", and the request's "id" if it had one. A failure has
// "error" with a code (the Err constants), a message that says what to do
// about it, and, for a step of a "do", its number.
//
// # The socket
//
// Listen creates a Unix domain socket (never TCP) with mode 0600 in a
// directory the user owns that neither the group nor others can write, with no
// ACL, under ancestors that others cannot rename it away in (CheckDir). A lock
// file next to the socket, flocked for the bridge's life, is what tells a live
// bridge from a dead one's leftover: a socket is removed as stale only when the
// lock can be taken, and the bridge's own is removed on Close. Shots are written
// only into a directory held to the same rule (OpenShotDest). Listen is off on
// Windows for now (errors.ErrUnsupported).
//
// # Threat model
//
// The bridge defends against other local users and against what a client
// sends. It assumes the socket directory and the shot directory cannot be
// modified by anyone but the user. It does not defend against races by the
// user's own processes, which can already delete or replace the socket, the
// lock file or the shots.
package inspectwire
