//go:build !windows

package securestore

func protectForCurrentUser([]byte) ([]byte, error) {
	return nil, ErrUnsupportedProtection
}

func unprotectForCurrentUser([]byte) ([]byte, error) {
	return nil, ErrUnsupportedProtection
}
