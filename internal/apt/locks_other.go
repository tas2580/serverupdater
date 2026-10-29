//go:build !linux

package apt

import "errors"

func checkSystemPackageLocks() error {
	return errors.New("Neustarts werden nur auf Linux unterstützt")
}
