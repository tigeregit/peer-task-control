package run

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func runProcess(cmd *exec.Cmd) (retErr error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create process job: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, windows.CloseHandle(job)) }()
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("configure process job: %w", err)
	}
	// The shell must not spawn children before it is assigned to the job.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.WaitDelay = 5 * time.Second
	var mu sync.Mutex
	cmd.Cancel = func() error {
		mu.Lock()
		defer mu.Unlock()
		return windows.TerminateJobObject(job, 1)
	}

	mu.Lock()
	err = cmd.Start()
	if err != nil {
		mu.Unlock()
		return err
	}
	process, setupErr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if setupErr == nil {
		setupErr = windows.AssignProcessToJobObject(job, process)
		setupErr = errors.Join(setupErr, windows.CloseHandle(process))
	}
	if setupErr == nil {
		setupErr = resumeProcessThread(uint32(cmd.Process.Pid))
	}
	mu.Unlock()
	if setupErr != nil {
		killErr := cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		return errors.Join(fmt.Errorf("attach process to cancellation job: %w", setupErr), killErr, cmd.Wait())
	}
	return cmd.Wait()
}

func resumeProcessThread(pid uint32) (retErr error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, windows.CloseHandle(snapshot)) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	err = windows.Thread32First(snapshot, &entry)
	for err == nil {
		if entry.OwnerProcessID != pid {
			err = windows.Thread32Next(snapshot, &entry)
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		count, resumeErr := windows.ResumeThread(thread)
		closeErr := windows.CloseHandle(thread)
		if resumeErr == nil && count != 1 {
			resumeErr = fmt.Errorf("unexpected suspend count %d for process %d", count, pid)
		}
		return errors.Join(resumeErr, closeErr)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	return fmt.Errorf("initial thread for process %d not found", pid)
}
