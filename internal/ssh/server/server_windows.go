//go:build windows
// +build windows

package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/ActiveState/termtest/conpty"
	"github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/sys/windows"
)

func ExecCmd(channel gossh.Channel, command string, ptyTerm string, isPty bool, winCh <-chan ssh.Window, cmd exec.Cmd, uid, gid uint64, username string) int {
	vsn := windows.RtlGetVersion()
	if vsn.MajorVersion < 10 {
		log.Println("Windows version too old to support shell")
		return 255
	}

	if command != "" {
		cmd.Args = append(cmd.Args, "/C", command)
	}

	if isPty {
		win := <-winCh
		cpty, err := conpty.New(int16(win.Width), int16(win.Height))
		if err != nil {
			log.Fatalf("Could not open a conpty terminal: %v", err)
		}
		defer cpty.Close()

		go func() {
			for win := range winCh {
				cpty.Resize(uint16(win.Width), uint16(win.Height))
			}
		}()

		pid, _, err := cpty.Spawn(
			cmd.Path,
			[]string{},
			&syscall.ProcAttr{
				Env: os.Environ(),
			},
		)

		if err != nil {
			log.Printf("failed to start command %v\n", err)
			return 255
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			log.Printf("failed to find process %v\n", err)
			return 255
		}

		defer process.Kill()

		go func() {
			defer channel.Close()
			io.Copy(channel, cpty.OutPipe())
		}()

		go func() {
			defer channel.Close()
			io.Copy(cpty.InPipe(), channel)
		}()

		ps, err := process.Wait()
		if err != nil {
			log.Println("Error waiting for process:", err)
			return 255
		}

		log.Printf("Session ended normally, exit code %d", ps.ExitCode())
		return ps.ExitCode()
	} else {
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

func StartChildProcess(ctx context.Context, s io.ReadWriteCloser, process, username string) error {
	switch process {
	case "sftp":
		server, err := sftp.NewServer(s)
		if err != nil {
			return fmt.Errorf("sftp server init error: %s", err)
		}

		if err := server.Serve(); err == io.EOF {
			server.Close()
		} else if err != nil {
			return fmt.Errorf("sftp server completed with error: %s", err)
		}

		return nil
	default:
		return fmt.Errorf("unknown process: %s", process)
	}
}
