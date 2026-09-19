//go:build !windows

package config

import "os"

func replaceConfigFile(tempPath, targetPath string) error {
	return os.Rename(tempPath, targetPath)
}
