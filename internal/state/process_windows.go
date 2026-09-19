//go:build windows

package state

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
	errorLockViolation      = syscall.Errno(33)
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = kernel32.NewProc("LockFileEx")
	procUnlockFile = kernel32.NewProc("UnlockFileEx")
)

func tryLockFile(f *os.File) (bool, error) {
	var overlapped syscall.Overlapped
	r1, _, callErr := procLockFileEx.Call(
		uintptr(syscall.Handle(f.Fd())),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r1 != 0 {
		return true, nil
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorLockViolation {
		return false, nil
	}
	if callErr == syscall.Errno(0) {
		return false, syscall.EINVAL
	}
	return false, callErr
}

func unlockFile(f *os.File) error {
	var overlapped syscall.Overlapped
	r1, _, callErr := procUnlockFile.Call(
		uintptr(syscall.Handle(f.Fd())),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r1 != 0 {
		return nil
	}
	if callErr == syscall.Errno(0) {
		return syscall.EINVAL
	}
	return callErr
}
