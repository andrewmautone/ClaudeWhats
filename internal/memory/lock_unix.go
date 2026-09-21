//go:build !windows

package memory

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock, blocking until it is free, as
// the python does with fcntl.flock(LOCK_EX).
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
