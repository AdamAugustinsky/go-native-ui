package fs

// Open opens path with the operating system's default application: a
// document in its viewer, a folder in the file manager. Call it on the UI
// thread. It returns once the request is handed to the OS; an error means
// the path does not exist or the OS refused.
func Open(path string) error { return openPath(path) }
