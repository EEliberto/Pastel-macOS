package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/majd/ipatool/v2/pkg/util/operatingsystem"
)

// In the pinned ipatool, downloadFile opens its destination only after all
// commerce/AMD requests finish, before sending the CDN GET. Use the public OS
// interface to release the authentication transaction at that exact boundary.
// Other filesystem calls (stat, mkdir, metadata patching) do not release it.
type transferOperatingSystem struct {
	operatingsystem.OperatingSystem
	once           sync.Once
	beforeTransfer func() error
	transferError  error
}

func (s *transferOperatingSystem) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	if flag&os.O_CREATE != 0 && flag&(os.O_RDWR|os.O_WRONLY) != 0 {
		s.once.Do(func() { s.transferError = s.beforeTransfer() })
		if s.transferError != nil {
			return nil, s.transferError
		}
	}
	return s.OperatingSystem.OpenFile(name, flag, perm)
}

func emitPhase(phase string) { fmt.Fprintln(os.Stderr, "@@PASTEL:phase="+phase) }

func appleAssetsCached() bool {
	root, err := os.UserCacheDir()
	if err != nil {
		return false
	}
	// This cache layout is part of the pinned upstream revision. We only use
	// it for progress text; ipatool still validates every asset's size/hash.
	root = filepath.Join(root, "ipatool", "sap", "apple-assets-macos-27-26A434-arm64-v4")
	for _, name := range []string{"AppleMediaServices", "commerce", "adid", "CoreFP", "CoreFP.icxs", "fairplayd", "FairPlayIOKit"} {
		if info, err := os.Stat(filepath.Join(root, name)); err != nil || info.Size() == 0 {
			return false
		}
	}
	return true
}
