//go:build windows

package baseline

import "golang.org/x/sys/windows"

func installFileAtomicallyNoReplace(source, target string) error {
	sourcePath, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPath, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePath, targetPath, windows.MOVEFILE_WRITE_THROUGH)
}
