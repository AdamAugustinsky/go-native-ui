package fs

// Open opens path with the operating system's default application: a
// document in its viewer, a folder in the file manager. It blocks until the
// request is handed to the OS, which can take long (a path on a dead network
// mount): do NOT call it on the UI thread; use (*Service).Open there. This
// blocking form is for tools and tests.
func Open(path string) error { return openPath(path) }
