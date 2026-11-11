package checkrun

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// pPID is waitid's idtype for a single process.
const pPID = 1

// untilExit returns once process pid, a child of factoryd's, exits, and
// leaves it a zombie: WNOWAIT keeps it for Wait to reap.
func untilExit(pid int) error {
	var info [16]uint64 // siginfo_t
	for {
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, pPID, uintptr(pid), uintptr(unsafe.Pointer(&info)), syscall.WEXITED|syscall.WNOWAIT, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		return nil
	}
}

// othersIn reports whether a process other than pgid itself, zombies
// included, is in process group pgid, by its stat in /proc.
func othersIn(pgid int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	want := strconv.Itoa(pgid)
	for _, e := range entries {
		if e.Name() == want {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // gone since it was listed
		}
		// The command name, in parentheses, may hold anything; state,
		// ppid and pgrp follow its closing one.
		stat := string(data)
		fields := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
		if len(fields) >= 3 && fields[2] == want {
			return true, nil
		}
	}
	return false, nil
}
