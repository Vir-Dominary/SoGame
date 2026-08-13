//go:build windows

package securestore

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var dpapiEntropy = []byte("sogame-netbird-room-code-v1")

func protectForCurrentUser(cleartext []byte) ([]byte, error) {
	input := dataBlob(cleartext)
	entropy := dataBlob(dpapiEntropy)
	description, err := windows.UTF16PtrFromString("Sogame NetBird Room Code")
	if err != nil {
		return nil, err
	}
	var output windows.DataBlob
	if err := windows.CryptProtectData(
		&input,
		description,
		&entropy,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	); err != nil {
		return nil, err
	}
	return copyAndFreeDataBlob(&output)
}

func unprotectForCurrentUser(ciphertext []byte) ([]byte, error) {
	input := dataBlob(ciphertext)
	entropy := dataBlob(dpapiEntropy)
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(
		&input,
		nil,
		&entropy,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&output,
	); err != nil {
		return nil, err
	}
	return copyAndFreeDataBlob(&output)
}

func dataBlob(value []byte) windows.DataBlob {
	if len(value) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
}

func copyAndFreeDataBlob(blob *windows.DataBlob) ([]byte, error) {
	if blob.Data == nil || blob.Size == 0 {
		return nil, errors.New("DPAPI returned an empty result")
	}
	allocated := unsafe.Slice(blob.Data, int(blob.Size))
	result := append([]byte(nil), allocated...)
	clearBytes(allocated)
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	blob.Data = nil
	blob.Size = 0
	return result, nil
}
