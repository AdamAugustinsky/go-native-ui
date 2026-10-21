//go:build !darwin

package dock

import "gonativeui/app"

func osSetBadge(string) error          { return ErrUnsupported }
func osBadge() string                  { return "" }
func osBounce(bool) (Attention, error) { return 0, ErrUnsupported }
func osCancelBounce(Attention)         {}
func osSetPolicy(app.Policy) error     { return ErrUnsupported }
func osPolicy() (app.Policy, error)    { return app.PolicyRegular, ErrUnsupported }
