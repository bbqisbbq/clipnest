//go:build windows

package clip

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Launching programs and running commands. These are the snippet "actions"; the
// trigger engine only asks for them through expand.Host.

// launchProgram starts an executable, honouring quotes and arguments.
// cmd /c start is used so "code ." or a path with spaces both work.
func launchProgram(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return errors.New("没有可启动的目标")
	}
	cmd := exec.Command("cmd", "/c", "start", "", spec)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// runCommand executes a shell command and returns the combined output.
func runCommand(line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("命令为空")
	}
	cmd := exec.Command("cmd", "/c", line)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return "", err
	}
	return text, nil
}

// ProcessImageName returns the executable name ("code.exe") owning hwnd, which is
// what the per-trigger app filters compare against.
func ProcessImageName(hwnd uintptr) string {
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
}
