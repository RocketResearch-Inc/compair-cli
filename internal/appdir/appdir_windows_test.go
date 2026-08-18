//go:build windows

package appdir

import (
	"errors"
	"testing"
)

func TestConfiguredApplicationRootFailsClosedOnWindows(t *testing.T) {
	t.Setenv(EnvironmentVariable, `C:\compair-private-state`)
	err := InitializeFromEnvironment()
	var appErr *Error
	if !errors.As(err, &appErr) || appErr.Reason != "platform_security_unsupported" {
		t.Fatalf("error = %#v", err)
	}
}
