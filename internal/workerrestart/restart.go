// Package workerrestart delegates an explicit worker restart to the user's
// service manager. The helper must survive the service it is restarting.
package workerrestart

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/iaia/telegramgw/internal/workerupdate"
	"golang.org/x/sys/unix"
)

type Runner = workerupdate.Runner

func service(kind string) (string, error) {
	switch kind {
	case "codex":
		return "codex-worker", nil
	case "claude":
		return "claude-worker", nil
	}
	return "", errors.New("unsupported worker kind")
}

// Schedule acknowledges scheduling, not completion of the eventual restart.
// The caller durably enters executing before calling this function and never
// replays an executing command, even if its acknowledgement is lost.
func Schedule(ctx context.Context, system, executable, stateFile, id, kind string, run Runner) error {
	name, err := service(kind)
	if err != nil {
		return err
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id || !filepath.IsAbs(executable) || !filepath.IsAbs(stateFile) {
		return errors.New("invalid restart target")
	}
	args := []string{executable, "restart-service"}
	switch system {
	case "linux":
		state, err := run(ctx, "systemctl", "--user", "show", name+".service", "--property=MainPID", "--value")
		if err != nil || strings.TrimSpace(string(state)) != strconv.Itoa(os.Getpid()) {
			return errors.New("this worker is not the installed user service")
		}
		launch := []string{"systemd-run", "--user", "--collect", "--unit=" + name + "-request-restart-" + id, "--property=Type=exec", "--property=UMask=0077", "--"}
		_, err = run(ctx, append(launch, args...)...)
		return err
	case "darwin":
		domain := "gui/" + strconv.Itoa(os.Getuid())
		state, err := run(ctx, "launchctl", "print", domain+"/com.iaia."+name)
		ownService := false
		for _, line := range strings.Split(string(state), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "pid" && fields[1] == "=" && fields[2] == strconv.Itoa(os.Getpid()) {
				ownService = true
				break
			}
		}
		if err != nil || !ownService {
			return errors.New("this worker is not the installed user service")
		}
		label := "com.iaia." + name + "-request-restart-" + id
		path := filepath.Join(stateFile+".restarts", id+".plist")
		var data bytes.Buffer
		data.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>`)
		_ = xml.EscapeText(&data, []byte(label))
		data.WriteString(`</string><key>ProgramArguments</key><array>`)
		for _, arg := range args {
			data.WriteString("<string>")
			_ = xml.EscapeText(&data, []byte(arg))
			data.WriteString("</string>")
		}
		// No KeepAlive: a failed or interrupted restart must never loop.
		data.WriteString(`</array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/></dict></plist>`)
		if err := workerupdate.WritePrivate(path, data.Bytes()); err != nil {
			return err
		}
		_, err = run(ctx, "launchctl", "bootstrap", domain, path)
		return err
	default:
		return errors.New("worker restart requires Linux systemd or macOS launchd")
	}
}

// RunHelper is invoked only by the service manager, outside the worker's
// service group. It accepts no remote executable, shell command or service name.
func RunHelper(ctx context.Context, system, kind string, run Runner) error {
	name, err := service(kind)
	if err != nil {
		return err
	}
	if system != "linux" && system != "darwin" {
		return errors.New("unsupported service manager")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	directory := filepath.Join(home, ".local", "state", name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(directory, "update.lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	info, err := lock.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("invalid worker update lock")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("worker update is in progress; restart was not performed")
	}
	if system == "linux" {
		_, err = run(ctx, "systemctl", "--user", "restart", name+".service")
	} else {
		_, err = run(ctx, "launchctl", "kickstart", "-k", "gui/"+strconv.Itoa(os.Getuid())+"/com.iaia."+name)
	}
	return err
}

// Run bounds service-manager calls without attaching the helper to the worker.
func Run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
}
