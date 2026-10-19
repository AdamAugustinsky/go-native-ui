//go:build windows

package instance

// checkPrivateDir does nothing on Windows: the directories used are under the
// user's profile, whose access control list already excludes other users.
func checkPrivateDir(string) error { return nil }
