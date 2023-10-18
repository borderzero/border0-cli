//go:build !windows
// +build !windows

package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/opencontainers/selinux/go-selinux"
	gossh "golang.org/x/crypto/ssh"
)

func ExecCmd(channel gossh.Channel, command string, ptyTerm string, isPty bool, winCh <-chan ssh.Window, cmd exec.Cmd, uid, gid uint64, username string) int {
	euid := os.Geteuid()
	var loginCmd string
	if selinux.EnforceMode() != selinux.Enforcing {
		loginCmd, _ = exec.LookPath("login")
	}
	sysProcAttr := &syscall.SysProcAttr{}

	if command != "" {
		if euid == 0 {
			err := syscall.Setgroups([]int{})
			if err != nil {
				log.Fatalf("Failed to clear supplementary group access list: %v", err)
			}
		}

		sysProcAttr.Credential = &syscall.Credential{
			Uid:         uint32(uid),
			Gid:         uint32(gid),
			NoSetGroups: true,
		}
		cmd.Args = append(cmd.Args, "-c", command)
	} else {
		if euid == 0 && loginCmd != "" {
			cmd.Path = loginCmd
			if hasBusyBoxLogin(loginCmd) {
				cmd.Args = []string{loginCmd, "-p", "-h", "Border0", "-f", username}
			} else {
				cmd.Args = append([]string{loginCmd, "-p", "-h", "Border0", "-f", username}, cmd.Args...)
			}
		} else {
			sysProcAttr.Credential = &syscall.Credential{
				Uid:         uint32(uid),
				Gid:         uint32(gid),
				NoSetGroups: true,
			}

			if euid == 0 {
				err := syscall.Setgroups([]int{})
				if err != nil {
					log.Fatalf("Failed to clear supplementary group access list: %v", err)
				}
			}

			cmd.Args = []string{fmt.Sprintf("-%s", cmd.Args[0])}
		}
	}

	if isPty {
		cmd.Env = append(cmd.Env, fmt.Sprintf("TERM=%s", ptyTerm))
		sysProcAttr.Setsid = true
		sysProcAttr.Setctty = true

		f, err := pty.StartWithAttrs(&cmd, &pty.Winsize{}, sysProcAttr)
		if err != nil {
			log.Println(err)
			return 255
		}

		go func() {
			for win := range winCh {
				setWinsize(f, win.Width, win.Height)
			}
		}()

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			io.Copy(f, channel)
			f.Close()
			if cmd.ProcessState == nil {
				cmd.Process.Signal(syscall.SIGKILL)
			}
		}()

		go func() {
			defer wg.Done()
			time.Sleep(200 * time.Millisecond)
			io.Copy(channel, f)
			channel.Close()
		}()

		wg.Wait()
		cmd.Wait()

		if cmd.ProcessState == nil {
			cmd.Process.Signal(syscall.SIGKILL)
		}

		return cmd.ProcessState.ExitCode()
	} else {
		sysProcAttr.Setsid = true
		cmd.SysProcAttr = sysProcAttr

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("failed to set stdout: %v\n", err)
			return 255
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			log.Printf("failed to set stderr: %v\n", err)
			return 255
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			log.Printf("failed to set stdin: %v\n", err)
			return 255
		}

		wg := &sync.WaitGroup{}
		wg.Add(2)
		if err = cmd.Start(); err != nil {
			log.Printf("failed to start command %v\n", err)
			return 255
		}
		go func() {
			defer stdin.Close()
			if _, err := io.Copy(stdin, channel); err != nil {
				log.Printf("failed to write to session %s\n", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := io.Copy(channel, stdout); err != nil {
				log.Printf("failed to write to stdout %s\n", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := io.Copy(channel.Stderr(), stderr); err != nil {
				log.Printf("failed to write from stderr%s\n", err)
			}
		}()

		wg.Wait()
		cmd.Wait()
		return cmd.ProcessState.ExitCode()
	}
}

func setWinsize(f *os.File, w, h int) {
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCSWINSZ),
		uintptr(unsafe.Pointer(&struct{ h, w, x, y uint16 }{uint16(h), uint16(w), 0, 0})))
}

func hasBusyBoxLogin(loginCmd string) bool {
	fileInfo, err := os.Lstat(loginCmd)
	if err != nil {
		return false
	}

	if fileInfo.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(loginCmd)
		if err != nil {
			return false
		}

		if filepath.Base(target) == "busybox" {
			return true
		}
	}

	return false
}

func StartChildProcess(ctx context.Context, s io.ReadWriteCloser, process, username string) error {
	user, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("could not find user %s: %v", username, err)
	}

	uidv, err := strconv.ParseInt(user.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("could not parse uid: %v", err)
	}
	uid := int(uidv)

	euid := os.Geteuid()
	if uid != euid && euid != 0 {
		return fmt.Errorf("need root privileges to start child process as another user")
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not get executable path: %s", err)
	}

	groups, err := user.GroupIds()
	if err != nil {
		return fmt.Errorf("could not get user groups: %s", err)
	}

	commandArgs := []string{"child", process, "--user", user.Username, "--uid", user.Uid, "--gid", user.Gid}
	if len(groups) > 0 {
		for _, group := range groups {
			commandArgs = append(commandArgs, "--group", group)
		}
	}

	cmd := exec.CommandContext(ctx, executable, commandArgs...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to set stdin: %v", err)
	}

	go func() {
		defer stdin.Close()
		if _, err := io.Copy(stdin, s); err != nil {
			log.Printf("failed to write to session %s\n", err)
		}
	}()

	cmd.Stdout = s
	cmd.Stderr = os.Stderr
	cmd.Dir = user.HomeDir

	return cmd.Run()
}
