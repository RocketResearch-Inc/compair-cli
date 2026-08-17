//go:build !windows

package baseline

import "os"

func installFileAtomicallyNoReplace(source, target string) error {
	if err := os.Link(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}
