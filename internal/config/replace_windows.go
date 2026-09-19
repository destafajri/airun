//go:build windows

package config

import (
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var (
	kernel32Config   = syscall.NewLazyDLL("kernel32.dll")
	moveFileExConfig = kernel32Config.NewProc("MoveFileExW")
)

func replaceConfigFile(tempPath, targetPath string) error {
	from, err := syscall.UTF16PtrFromString(tempPath)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(targetPath)
	if err != nil {
		return err
	}
	r1, _, callErr := moveFileExConfig.Call(
		uintptr(unsafe.Pointer(from)),
		uintptr(unsafe.Pointer(to)),
		uintptr(moveFileReplaceExisting|moveFileWriteThrough),
	)
	if r1 != 0 {
		return nil
	}
	if callErr != nil && callErr != syscall.Errno(0) {
		return callErr
	}
	return syscall.EINVAL
}
