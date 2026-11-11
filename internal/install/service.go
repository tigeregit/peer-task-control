package install

import (
	"fmt"
	"strings"

	"github.com/tigeregit/peer-task-control/internal/selfupdate"
)

func unitText(l Layout, name string, system bool) string {
	wanted := "default.target"
	if system {
		wanted = "multi-user.target"
	}
	return fmt.Sprintf(`[Unit]
Description=peer-task-control daemon (%s)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s daemon --config %s --data-dir %s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=%s
`, name, l.Bin, l.Config, l.DataDir, wanted)
}

// installService registers and (re)starts the daemon. Order of preference
// on Linux: systemd (system unit for --system, else user unit), then a
// detached nohup process when the host has no systemd (containers).
func installService(c *Conn, l Layout, o Options, name string, binChanged bool) (string, error) {
	if l.Platform.OS == "windows" {
		return installSchtask(c, l, o, name, binChanged)
	}
	sudo := ""
	if l.Sudo {
		sudo = "sudo -n "
	}
	if l.System {
		if _, _, err := c.Run(sudo + "systemctl is-system-running >/dev/null 2>&1 || " + sudo + "test -d /run/systemd/system"); err == nil {
			unit := "/etc/systemd/system/" + name + ".service"
			text := unitText(l, name, true)
			changed := remoteSHA256(c, l, unit) != sum([]byte(text))
			if changed && !o.DryRun {
				if err := c.put(l, unit, []byte(text), 0o644); err != nil {
					return "", err
				}
			}
			act := "restart"
			if !changed && !binChanged {
				act = "start"
			}
			cmd := fmt.Sprintf("%ssystemctl daemon-reload && %ssystemctl enable %s && %ssystemctl %s %s", sudo, sudo, name, sudo, act, name)
			if !o.DryRun {
				if _, se, err := c.Run(cmd); err != nil {
					return "", fmt.Errorf("systemd: %v %s", err, se)
				}
			}
			rep := fmt.Sprintf("%s (system unit, %s)", unit, act)
			logStep(o, "service", rep)
			return selfupdate.ServiceSystemdSystem, nil
		}
	} else if _, _, err := c.Run("systemctl --user show-environment >/dev/null 2>&1"); err == nil {
		unit := l.Home + "/.config/systemd/user/" + name + ".service"
		text := unitText(l, name, false)
		changed := remoteSHA256(c, l, unit) != sum([]byte(text))
		if changed && !o.DryRun {
			if err := c.mkdirAll(l, l.Home+"/.config/systemd/user"); err != nil {
				return "", err
			}
			if err := c.put(l, unit, []byte(text), 0o644); err != nil {
				return "", err
			}
		}
		act := "restart"
		if !changed && !binChanged {
			act = "start"
		}
		cmd := fmt.Sprintf("systemctl --user daemon-reload && systemctl --user enable %s && systemctl --user %s %s", name, act, name)
		if !o.DryRun {
			if _, se, err := c.Run(cmd); err != nil {
				return "", fmt.Errorf("systemd --user: %v %s", err, se)
			}
			// Keep the user manager (and the daemon) running after logout.
			_, _, _ = c.Run("loginctl enable-linger \"$(id -un)\" 2>/dev/null")
		}
		logStep(o, "service", fmt.Sprintf("%s (user unit, %s; linger enabled when allowed)", unit, act))
		return selfupdate.ServiceSystemdUser, nil
	}
	// No systemd: detached process supervised by ptc itself (update restarts it).
	pidFile := l.DataDir + "/daemon.pid"
	running := false
	if out, _, err := c.Run(sudo + "sh -c " + sq(`p=$(cat `+sq(pidFile)+` 2>/dev/null) && [ -n "$p" ] && kill -0 "$p" 2>/dev/null && echo yes`)); err == nil && strings.TrimSpace(out) == "yes" {
		running = true
	}
	if running && !binChanged {
		logStep(o, "service", "daemon already running (nohup, pid file "+pidFile+")")
		return selfupdate.ServiceNohup, nil
	}
	start := fmt.Sprintf(`p=$(cat %[1]s 2>/dev/null); if [ -n "$p" ] && kill -0 "$p" 2>/dev/null; then kill "$p"; for i in 1 2 3 4 5 6 7 8 9 10; do kill -0 "$p" 2>/dev/null || break; sleep 0.5; done; fi; `+
		`mkdir -p %[2]s && cd %[2]s && (setsid nohup %[3]s daemon --config %[4]s --data-dir %[2]s >> %[5]s 2>&1 < /dev/null & echo $! > %[1]s)`,
		sq(pidFile), sq(l.DataDir), sq(l.Bin), sq(l.Config), sq(l.Log))
	if !o.DryRun {
		if _, se, err := c.Run(sudo + "sh -c " + sq(start)); err != nil {
			return "", fmt.Errorf("start daemon: %v %s", err, se)
		}
	}
	logStep(o, "service", "no systemd; started detached daemon (nohup, pid file "+pidFile+", log "+l.Log+")")
	return selfupdate.ServiceNohup, nil
}

func installSchtask(c *Conn, l Layout, o Options, name string, binChanged bool) (string, error) {
	tr := fmt.Sprintf(`"%s" daemon --config "%s" --data-dir "%s"`, l.Bin, l.Config, l.DataDir)
	exists := false
	if _, _, err := c.Run("schtasks /Query /TN " + name); err == nil {
		exists = true
	}
	if !o.DryRun {
		if !exists || binChanged {
			cmd := ps(fmt.Sprintf(`schtasks /Create /F /TN %s /SC ONLOGON /RL LIMITED /TR %s | Out-Null; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }; schtasks /End /TN %s 2>$null | Out-Null; schtasks /Run /TN %s | Out-Null; exit $LASTEXITCODE`, psq(name), psq(tr), psq(name), psq(name)))
			if _, se, err := c.Run(cmd); err != nil {
				return "", fmt.Errorf("schtasks: %v %s", err, se)
			}
		}
	}
	logStep(o, "service", fmt.Sprintf("scheduled task %q (on logon) -> %s", name, tr))
	return selfupdate.ServiceSchtasks, nil
}

func logStep(o Options, action, detail string) {
	if o.Log == nil {
		return
	}
	mark := "ok  "
	if o.DryRun {
		mark = "plan"
	}
	fmt.Fprintf(o.Log, "  %s %-10s %s\n", mark, action, detail)
}
