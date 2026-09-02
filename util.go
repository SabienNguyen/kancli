package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// timeNow is replaceable in tests.
var timeNow = time.Now

// openExternal opens a URL or file with the desktop's default handler.
func openExternal(ref string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", ref)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", ref)
	default:
		cmd = exec.Command("xdg-open", ref)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", ref, err)
	}
	go cmd.Wait() //nolint:errcheck // detached
	return nil
}

// psString quotes text as a PowerShell single-quoted literal, in which the
// only special character is the quote itself. Nothing is expanded.
func psString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// notifyDesktop shows a desktop notification using whatever the platform
// provides. It returns an error when no notifier is available.
func notifyDesktop(title, body string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// The text is passed as arguments, never interpolated into the script.
		cmd = exec.Command("osascript",
			"-e", "on run argv",
			"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
			"-e", "end run",
			body, title)
	case "windows":
		ps := `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null; ` +
			`$t = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); ` +
			`$x = $t.GetXml(); $x.GetElementsByTagName('text')[0].AppendChild($x.CreateTextNode(` + psString(title) + `)) | Out-Null; ` +
			`$x.GetElementsByTagName('text')[1].AppendChild($x.CreateTextNode(` + psString(body) + `)) | Out-Null; ` +
			`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('kancli').Show([Windows.UI.Notifications.ToastNotification]::new($t))`
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	default:
		if _, err := exec.LookPath("notify-send"); err != nil {
			return fmt.Errorf("notify-send not found")
		}
		cmd = exec.Command("notify-send", title, body)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("notification failed: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
