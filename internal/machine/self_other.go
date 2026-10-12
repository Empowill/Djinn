//go:build !linux && !darwin

package machine

import "errors"

// ReadSelf reads what Djinn's own process uses: not measured here.
func ReadSelf() (Group, error) { return Group{}, errors.New(NotMeasured) }
