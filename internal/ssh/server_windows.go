//go:build windows
// +build windows

package ssh

import (
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
	"golang.org/x/sys/windows"
)

func execCmd(s ssh.Session, cmd exec.Cmd, uid, gid uint64, username string) {
	ptyReq, winCh, isPty := s.Pty()

	vsn := windows.RtlGetVersion()
	if vsn.MajorVersion < 10 {
		log.Println("Windows version too old to support shell")
		return
	}

	if len(s.Command()) > 0 {
		cmd.Args = append(cmd.Args, "/C", s.RawCommand())
	}

	if isPty {

		cpty, err := conpty.New(int16(ptyReq.Window.Width), int16(ptyReq.Window.Height))
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
			return
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			log.Printf("failed to find process %v\n", err)
			return
		}

		defer process.Kill()

		go func() {
			io.Copy(s, cpty.OutPipe())
			s.Close()
		}()
		go func() {
			io.Copy(cpty.InPipe(), s)
			s.Close()
		}()

		done := make(chan struct {
			*os.ProcessState
			error
		}, 1)
		go func() {
			ps, err := process.Wait()
			done <- struct {
				*os.ProcessState
				error
			}{ps, err}
		}()

		select {
		case result := <-done:
			if result.error != nil {
				log.Println("Error waiting for process:", err)
				s.Exit(255)
				return
			}
			log.Printf("Session ended normally, exit code %d", result.ProcessState.ExitCode())
			s.Exit(result.ProcessState.ExitCode())
			return

		case <-s.Context().Done():
			log.Printf("Session terminated: %s", s.Context().Err())
			return
		}

	} else {

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("failed to set stdout: %v\n", err)
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			log.Printf("failed to set stderr: %v\n", err)
			return
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			log.Printf("failed to set stdin: %v\n", err)
			return
		}

		wg := &sync.WaitGroup{}
		wg.Add(2)
		if err = cmd.Start(); err != nil {
			log.Printf("failed to start command %v\n", err)
			return
		}
		go func() {
			defer stdin.Close()
			if _, err := io.Copy(stdin, s); err != nil {
				log.Printf("failed to write to session %s\n", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := io.Copy(s, stdout); err != nil {
				log.Printf("failed to write to stdout %s\n", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := io.Copy(s.Stderr(), stderr); err != nil {
				log.Printf("failed to write from stderr%s\n", err)
			}
		}()

		wg.Wait()
		cmd.Wait()
	}
}

func startChildProcess(s ssh.Session, process, username string) error {
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
