package checkrun

import (
	"encoding/binary"
	"errors"
	"syscall"
	"unsafe"
)

// untilExit returns once process pid, a child of factoryd's, exits, and
// leaves it a zombie: a kqueue NOTE_EXIT reaps nothing. One that already
// exited fails the registration with ESRCH, as no other pid can: this one
// is not reaped yet.
func untilExit(pid int) error {
	kq, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	defer func() { _ = syscall.Close(kq) }()
	var ev syscall.Kevent_t
	syscall.SetKevent(&ev, pid, syscall.EVFILT_PROC, syscall.EV_ADD|syscall.EV_ONESHOT)
	ev.Fflags = syscall.NOTE_EXIT
	out := make([]syscall.Kevent_t, 1)
	for {
		n, err := syscall.Kevent(kq, []syscall.Kevent_t{ev}, out, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 1 && out[0].Flags&syscall.EV_ERROR != 0 && out[0].Data != 0 {
			if errno := syscall.Errno(out[0].Data); errno != syscall.ESRCH {
				return errno
			}
		}
		return nil
	}
}

// The sysctl names and the layout of struct kinfo_proc, from <sys/sysctl.h>
// and <sys/proc.h>, the same on amd64 and arm64.
const (
	ctlKern      = 1
	kernProc     = 14
	kernProcPgrp = 2
	kinfoSize    = 648
	kinfoPidAt   = 40
)

// othersIn reports whether a process other than pgid itself, zombies
// included, is in process group pgid, by sysctl kern.proc.pgrp.
func othersIn(pgid int) (bool, error) {
	mib := [4]int32{ctlKern, kernProc, kernProcPgrp, int32(pgid)}
	for {
		var size uintptr
		if err := sysctl(mib[:], nil, &size); err != nil {
			return false, err
		}
		// Room for a few more, should the group grow between the calls.
		size += 8 * kinfoSize
		buf := make([]byte, size)
		err := sysctl(mib[:], &buf[0], &size)
		if errors.Is(err, syscall.ENOMEM) {
			continue
		}
		if err != nil {
			return false, err
		}
		for at := 0; at+kinfoSize <= int(size); at += kinfoSize {
			if int(int32(binary.LittleEndian.Uint32(buf[at+kinfoPidAt:]))) != pgid {
				return true, nil
			}
		}
		return false, nil
	}
}

func sysctl(mib []int32, old *byte, size *uintptr) error {
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(old)), uintptr(unsafe.Pointer(size)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
