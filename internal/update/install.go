package update

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Apply installs a prepared artifact over the running executable.
//
// When the install directory is writable the replacement happens right here
// and done is true. When it is not, done is false and cmd is an elevation
// command (sudo on Unix, a UAC prompt on Windows) for the caller to run with
// the terminal handed over, so the password prompt has somewhere to appear.
func (u *Updater) Apply(a *Artifact) (done bool, cmd *exec.Cmd, err error) {
	if a == nil || a.Path == "" {
		return false, nil, errors.New("no update has been prepared")
	}

	target, err := u.target()
	if err != nil {
		return false, nil, err
	}

	if err := Replace(a.Path, target); err != nil {
		if errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrPermission) {
			cmd, cerr := elevatedInstall(a.Path, target)
			if cerr != nil {
				return false, nil, cerr
			}
			return false, cmd, nil
		}
		return false, nil, err
	}
	return true, nil, nil
}

func (u *Updater) target() (string, error) {
	fn := u.Executable
	if fn == nil {
		fn = executablePath
	}
	return fn()
}

func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// Installed through a symlink, the real file is the one to replace.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

// Replace copies source over target, staging the copy beside it so the final
// rename is atomic. On Windows the running image cannot be overwritten, but it
// can be renamed out of the way first.
func Replace(source, target string) error {
	staged := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".new")
	if err := copyFile(source, staged); err != nil {
		_ = os.Remove(staged)
		return err
	}

	if runtime.GOOS != "windows" {
		return os.Rename(staged, target)
	}

	old := target + ".old"
	_ = os.Remove(old)
	if err := os.Rename(target, old); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(staged)
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(old, target)
		return err
	}
	// The old image is still mapped by this process, so deletion may have to
	// wait for the next launch (see CleanupOldExecutable).
	_ = os.Remove(old)
	return nil
}

func copyFile(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, 0o755)
}

// CleanupOldExecutable removes a Windows backup left behind when the running
// image could not be deleted during an update.
func CleanupOldExecutable() {
	exe, err := executablePath()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")
}

func elevatedInstall(source, target string) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		return elevatedInstallWindows(source, target)
	}
	return exec.Command("sudo", "install", "-m", "0755", source, target), nil
}

// elevatedInstallWindows writes two small scripts beside the downloaded
// binary: one that swaps the files, and one that asks UAC to run the first.
// Keeping the paths in a file avoids quoting them through three layers of
// PowerShell.
func elevatedInstallWindows(source, target string) (*exec.Cmd, error) {
	dir := filepath.Dir(source)
	applyPath := filepath.Join(dir, "apply.ps1")
	elevatePath := filepath.Join(dir, "elevate.ps1")

	apply := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$target = %s
$source = %s
$old = $target + '.old'
Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
if (Test-Path -LiteralPath $target) {
    Move-Item -LiteralPath $target -Destination $old -Force
}
try {
    Copy-Item -LiteralPath $source -Destination $target -Force
} catch {
    if ((-not (Test-Path -LiteralPath $target)) -and (Test-Path -LiteralPath $old)) {
        Move-Item -LiteralPath $old -Destination $target -Force
    }
    throw
}
Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
`, psQuote(target), psQuote(source))

	// Start-Process joins an array argument with spaces without quoting it,
	// so build one pre-quoted command line instead. A user name with a space
	// must not split the script path.
	elevate := fmt.Sprintf(`$arguments = '-NoProfile -ExecutionPolicy Bypass -File "' + %s + '"'
$p = Start-Process -Verb RunAs -Wait -PassThru -WindowStyle Hidden `+
		`-FilePath 'powershell.exe' -ArgumentList $arguments
exit $p.ExitCode
`, psQuote(applyPath))

	if err := os.WriteFile(applyPath, []byte(apply), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(elevatePath, []byte(elevate), 0o600); err != nil {
		return nil, err
	}
	return exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", elevatePath), nil
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
