//go:build !windows

package baseline

import "os"

func replaceFileAtomically(source, target string) error {
	return os.Rename(source, target)
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return uploadError(UploadFailureInternal, "state_write_failed")
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return uploadError(UploadFailureInternal, "state_write_failed")
	}
	return nil
}

func ensurePrivateDirectoryPermissions(directory string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	return os.Chmod(directory, 0o700)
}

func privateFilePermissions(info os.FileInfo) bool {
	return info.Mode().Perm()&0o077 == 0
}
