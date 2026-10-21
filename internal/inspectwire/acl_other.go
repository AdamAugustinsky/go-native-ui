//go:build !windows && !darwin && !linux

package inspectwire

func hasACL(string) (bool, error)            { return false, nil }
func stripACL(string) error                  { return nil }
func dirHasACL(string) (bool, error)         { return false, nil }
func aclGrantsMutation(string) (bool, error) { return false, nil }
