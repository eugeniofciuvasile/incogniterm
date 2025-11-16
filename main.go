// incogniterm - Disposable fake-identity terminal for demos and recordings.
// Date: 17-11-2025
// Author: eugeniofciuvasile
//
// Description:
// incogniterm starts the user's preferred shell inside a pseudo-terminal (PTY),
// with a randomly generated fake username and hostname and an ephemeral HOME
// directory. It creates temporary shell configuration and history files and
// injects lightweight wrapper commands (id, whoami, hostname) so that common
// identity-related commands report the fake identity. The temporary HOME and
// its contents are deleted automatically when incogniterm exits.
//
// It is designed for teaching, demos, and recordings where the user wants to
// hide their real identity and avoid polluting their real shell configuration
// and history. It does not provide security isolation and still has access to
// the real filesystem and user permissions.
//
// It is intended to be portable across Unix-like operating systems that
// support pseudo-terminals (Linux, macOS, *BSD). Windows support depends on
// the availability of the required PTY and terminal APIs.

package main

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/creack/pty"
	"golang.org/x/term"
)

func main() {
	seedRandom()
	gofakeit.Seed(time.Now().UnixNano())

	origDir, err := getWorkingDirectory()
	if err != nil {
		log.Fatalf("failed to get current dir: %v", err)
	}

	shell, shellBase := resolveShell()

	tmpHome, err := createIncognitermHome()
	if err != nil {
		log.Fatalf("failed to create temp home: %v", err)
	}
	defer os.RemoveAll(tmpHome)

	fakeUser, fakeHost := generateFakeIdentity()
	ps1 := buildPrompt(fakeUser, fakeHost)

	tmpBin, err := createTempBin(tmpHome)
	if err != nil {
		log.Fatalf("failed to create temp bin: %v", err)
	}

	if err := writeIdentityWrappers(tmpBin, fakeUser, fakeHost); err != nil {
		log.Fatalf("failed to write identity wrappers: %v", err)
	}

	rcFile, err := writeShellRC(shellBase, tmpHome, fakeUser, fakeHost, ps1)
	if err != nil {
		log.Fatalf("failed to write shell rc: %v", err)
	}

	env := buildEnvironment(fakeUser, fakeHost, tmpHome, tmpBin)
	cmd := buildShellCommand(shell, shellBase, rcFile, tmpHome, env)

	if err := changeDirectory(tmpHome); err != nil {
		log.Printf("warning: failed to chdir to temp home: %v", err)
	}

	ptmx, err := startPTY(cmd, origDir)
	if err != nil {
		log.Fatalf("failed to start pty: %v", err)
	}
	defer ptmx.Close()

	setupWindowResize(ptmx)
	oldState, err := setTerminalRaw(origDir)
	if err != nil {
		log.Fatalf("failed to set raw mode: %v", err)
	}
	defer restoreTerminalAndDirectory(oldState, origDir)

	startIOCopy(ptmx)
	runShellAndExit(cmd)
}

// seedRandom initializes the math/rand global source with the current time.
// It is used to produce non-cryptographic random values for names and hostnames.
func seedRandom() {
	seed := time.Now().UnixNano()
	_ = rand.NewSource(seed)
}

// getWorkingDirectory returns the current working directory of the process.
// It is used to restore the directory after the incogniterm session ends.
func getWorkingDirectory() (string, error) {
	return os.Getwd()
}

// resolveShell determines the shell executable to use and its base name.
// It prefers the SHELL environment variable and falls back to /bin/bash.
func resolveShell() (string, string) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return shell, filepath.Base(shell)
}

// createIncognitermHome creates a temporary directory to be used as HOME.
// It returns the created directory path, which is intended to be deleted on exit.
func createIncognitermHome() (string, error) {
	return os.MkdirTemp("", "incogniterm-home-*")
}

// generateFakeIdentity produces a fake username and hostname using gofakeit.
// It returns the fake user and fake host as strings.
func generateFakeIdentity() (string, string) {
	fakeFirst := strings.ToLower(gofakeit.FirstName())
	fakeLast := strings.ToLower(gofakeit.LastName())
	fakeUser := fmt.Sprintf("%s_%s", fakeFirst, fakeLast)

	fakeCity := strings.ToLower(strings.ReplaceAll(gofakeit.City(), " ", "-"))
	fakeHost := fmt.Sprintf("%s-node-%d", fakeCity, rand.Intn(9000)+1000)

	return fakeUser, fakeHost
}

// buildPrompt constructs a shell prompt string using the fake user and host.
// It returns a bash-style PS1 prompt.
func buildPrompt(fakeUser, fakeHost string) string {
	return fmt.Sprintf("[%s@%s \\w]\\$ ", fakeUser, fakeHost)
}

// createTempBin creates a bin directory under the given home path.
// It returns the full path to the bin directory.
func createTempBin(home string) (string, error) {
	tmpBin := filepath.Join(home, "bin")
	err := os.MkdirAll(tmpBin, 0o755)
	return tmpBin, err
}

// writeIdentityWrappers writes lightweight wrapper scripts for id, whoami,
// and hostname into the specified bin directory so that they report the fake
// identity when executed.
func writeIdentityWrappers(binDir, fakeUser, fakeHost string) error {
	idScript := fmt.Sprintf(`#!/bin/sh
echo "uid=1000(%[1]s) gid=1000(%[1]s) groups=1000(%[1]s)"
`, fakeUser)

	whoamiScript := fmt.Sprintf(`#!/bin/sh
echo "%s"
`, fakeUser)

	hostnameScript := fmt.Sprintf(`#!/bin/sh
echo "%s"
`, fakeHost)

	if err := os.WriteFile(filepath.Join(binDir, "id"), []byte(idScript), 0o755); err != nil {
		return fmt.Errorf("write fake id: %w", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "whoami"), []byte(whoamiScript), 0o755); err != nil {
		return fmt.Errorf("write fake whoami: %w", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "hostname"), []byte(hostnameScript), 0o755); err != nil {
		return fmt.Errorf("write fake hostname: %w", err)
	}
	return nil
}

// writeShellRC creates an appropriate shell configuration file in the
// temporary home for the given shell. It returns the rc file path.
func writeShellRC(shellBase, home, fakeUser, fakeHost, ps1 string) (string, error) {
	switch shellBase {
	case "bash":
		rcFile := filepath.Join(home, ".bashrc")
		rcContent := fmt.Sprintf(`
export USER=%[1]s
export LOGNAME=%[1]s
export HOSTNAME=%[2]s
export PS1='%[3]s'
export HISTFILE="%[4]s/.bash_history"
`, fakeUser, fakeHost, ps1, home)
		if err := os.WriteFile(rcFile, []byte(rcContent), 0o600); err != nil {
			return "", err
		}
		return rcFile, nil

	case "zsh":
		rcFile := filepath.Join(home, ".zshrc")
		rcContent := fmt.Sprintf(`
export USER=%[1]s
export LOGNAME=%[1]s
export HOSTNAME=%[2]s
export HISTFILE="%[3]s/.zsh_history"
PROMPT='%%F{cyan}[%[1]s@%[2]s %%~]%%f$ '
`, fakeUser, fakeHost, home)
		if err := os.WriteFile(rcFile, []byte(rcContent), 0o600); err != nil {
			return "", err
		}
		return rcFile, nil

	default:
		rcFile := filepath.Join(home, ".bashrc")
		rcContent := fmt.Sprintf(`
export USER=%[1]s
export LOGNAME=%[1]s
export HOSTNAME=%[2]s
export PS1='%[3]s'
export HISTFILE="%[4]s/.bash_history"
`, fakeUser, fakeHost, ps1, home)
		if err := os.WriteFile(rcFile, []byte(rcContent), 0o600); err != nil {
			return "", err
		}
		return rcFile, nil
	}
}

// buildEnvironment constructs the environment for the incogniterm shell
// by overriding USER, LOGNAME, HOME, HOSTNAME and prepending the binDir
// to PATH.
func buildEnvironment(fakeUser, fakeHost, home, binDir string) []string {
	env := os.Environ()
	env = overrideEnv(env, "USER", fakeUser)
	env = overrideEnv(env, "LOGNAME", fakeUser)
	env = overrideEnv(env, "HOME", home)
	env = overrideEnv(env, "HOSTNAME", fakeHost)
	env = prependPath(env, binDir)
	return env
}

// buildShellCommand creates the exec.Cmd for the shell with appropriate
// arguments, environment, and working directory based on the shell type.
func buildShellCommand(shell, shellBase, rcFile, home string, env []string) *exec.Cmd {
	var cmd *exec.Cmd
	switch shellBase {
	case "bash":
		cmd = exec.Command(shell, "--rcfile", rcFile, "-i")
	case "zsh":
		cmd = exec.Command(shell, "-i")
	default:
		cmd = exec.Command(shell, "-i")
	}
	cmd.Env = env
	cmd.Dir = home
	return cmd
}

// changeDirectory changes the process working directory to the specified path.
// It is used to enter the temporary HOME before starting the shell.
func changeDirectory(dir string) error {
	return os.Chdir(dir)
}

// startPTY starts the given command attached to a pseudo-terminal.
// On failure, it restores the original directory and returns an error.
func startPTY(cmd *exec.Cmd, origDir string) (*os.File, error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = os.Chdir(origDir)
		return nil, err
	}
	return ptmx, nil
}

// setupWindowResize installs a SIGWINCH handler that keeps the PTY size
// in sync with the parent terminal window size.
func setupWindowResize(ptmx *os.File) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	ch <- syscall.SIGWINCH
}

// setTerminalRaw puts the parent terminal into raw mode and returns the
// previous state so that it can be restored later. On error, it restores
// the original working directory.
func setTerminalRaw(origDir string) (*term.State, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		_ = os.Chdir(origDir)
		return nil, err
	}
	return oldState, nil
}

// restoreTerminalAndDirectory restores the parent terminal to its previous
// state and changes the working directory back to origDir.
func restoreTerminalAndDirectory(oldState *term.State, origDir string) {
	_ = term.Restore(int(os.Stdin.Fd()), oldState)
	_ = os.Chdir(origDir)
}

// startIOCopy starts copying data between stdin and the PTY and then from
// the PTY to stdout. It runs the stdin->PTY copy in a goroutine.
func startIOCopy(ptmx *os.File) {
	go func() {
		_, _ = io.Copy(ptmx, os.Stdin)
	}()
	_, _ = io.Copy(os.Stdout, ptmx)
}

// runShellAndExit waits for the shell command to finish and then exits
// the incogniterm process with the same exit code, if available.
func runShellAndExit(cmd *exec.Cmd) {
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		log.Printf("shell exited with error: %v", err)
	}
}

// overrideEnv sets or replaces an environment variable in the provided
// slice of "key=value" strings and returns the updated slice.
func overrideEnv(env []string, key, value string) []string {
	prefix := key + "="
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			found = true
		}
	}
	if !found {
		env = append(env, prefix+value)
	}
	return env
}

// prependPath prepends the specified directory to the PATH variable in
// the provided environment slice and returns the updated slice.
func prependPath(env []string, dir string) []string {
	prefix := "PATH="
	for i, e := range env {
		if after, ok := strings.CutPrefix(e, prefix); ok {
			env[i] = prefix + dir + string(os.PathListSeparator) + after
			return env
		}
	}
	env = append(env, "PATH="+dir)
	return env
}
