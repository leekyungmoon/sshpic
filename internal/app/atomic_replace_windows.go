//go:build windows

package app

import (
	"errors"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
	windowsSharingViolation = syscall.Errno(32)
	windowsLockViolation    = syscall.Errno(33)

	// Windows can transiently deny a rename while another process is opening
	// the destination for metadata inspection without FILE_SHARE_DELETE. Keep
	// retrying the same atomic operation for a bounded period; never fall back
	// to remove-then-rename because that would expose a missing ledger.
	atomicReplaceRetryAttempts = 200
	atomicReplaceRetryDelay    = 5 * time.Millisecond
)

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFileAtomic(source, target string) error {
	sourcePtr, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	targetPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < atomicReplaceRetryAttempts; attempt++ {
		result, _, callErr := moveFileExW.Call(
			uintptr(unsafe.Pointer(sourcePtr)),
			uintptr(unsafe.Pointer(targetPtr)),
			uintptr(moveFileReplaceExisting|moveFileWriteThrough),
		)
		if result != 0 {
			return nil
		}
		if callErr == nil || callErr == syscall.Errno(0) {
			return os.ErrInvalid
		}
		lastErr = callErr
		if !retryableAtomicReplaceError(callErr) {
			return callErr
		}
		if attempt+1 < atomicReplaceRetryAttempts {
			time.Sleep(atomicReplaceRetryDelay)
		}
	}
	return lastErr
}

func retryableAtomicReplaceError(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windowsSharingViolation) ||
		errors.Is(err, windowsLockViolation)
}
