package main

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const childProcessEnv = "PUSAQ_TEST_CHILD_PROCESS"

func TestChildProcessRunsMain(t *testing.T) {
	if os.Getenv(childProcessEnv) != "1" {
		t.Skip("runs only as the child process of TestSecondSignalExitsImmediately")
	}
	main()
}

func TestSecondSignalExitsImmediately(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildProcessRunsMain$")
	cmd.Env = []string{
		childProcessEnv + "=1",
		"PUSAQ_DEV_MODE=true",
		"PUSAQ_ADDR=127.0.0.1:0",
		"PUSAQ_DRAIN_DELAY=1m",
		"PUSAQ_SHUTDOWN_GRACE=1s",
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	lines := make(chan string, 1024)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
		done <- cmd.Wait()
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	waitFor := func(msg string) {
		t.Helper()
		timeout := time.After(10 * time.Second)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("child exited before logging %q", msg)
				}
				if strings.Contains(line, `"msg":"`+msg+`"`) {
					return
				}
			case <-timeout:
				t.Fatalf("child did not log %q", msg)
			}
		}
	}
	waitFor("gateway listening")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitFor("draining")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	go func() {
		for range lines {
		}
	}()
	select {
	case err := <-done:
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatalf("child ended with %v, want termination by the second signal", err)
		}
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
			t.Fatalf("child status = %v, want killed by SIGTERM", exitErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second SIGTERM did not end a one-minute drain")
	}
}
