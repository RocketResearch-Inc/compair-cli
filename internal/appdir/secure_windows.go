//go:build windows

package appdir

func secureConfiguredRoot(_ string) error {
	return invalid("platform_security_unsupported")
}
