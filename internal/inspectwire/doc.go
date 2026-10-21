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
// directory the user owns that neither the group nor others can write. It
// removes a stale socket only if it is a socket the user owns that nobody
// answers on, and it removes its own on Close. On Windows, where files have no
// owner or mode to read, the directory must be inside the user's profile.
package inspectwire
