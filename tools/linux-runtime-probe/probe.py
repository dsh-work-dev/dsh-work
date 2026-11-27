"""Probe Linux facilities; never changes global systemd, sandbox or policy.

The caller compiles xres.c and runs this under its X11 test session. The real
systemd user bus is probed separately from dbus-run-session's private bus.
"""
import argparse
import json
import os
from pathlib import Path
import select
import shutil
import stat
import subprocess
import tempfile
import time
import uuid


def run(args, env=None, timeout=8):
    value = subprocess.run(args, env=env, text=True, capture_output=True,
                           timeout=timeout, check=False)
    return {"code": value.returncode, "stdout": value.stdout.strip(),
            "stderr": value.stderr.strip()}


def systemd_probe():
    result = {"qualified": False, "cleanupComplete": True}
    runtime = Path(os.environ.get("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}"))
    bus = runtime / "bus"
    result["runtimeDirectoryExists"] = runtime.is_dir()
    result["userBusSocketExists"] = bus.exists()
    if not runtime.is_dir() or not bus.exists():
        result["reason"] = "real systemd user-manager bus unavailable"
        return result
    info = runtime.stat()
    if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        result["reason"] = "runtime directory ownership/mode unsuitable"
        return result
    if not shutil.which("systemctl") or not shutil.which("systemd-run") or not shutil.which("setsid"):
        result["reason"] = "systemd user client or setsid unavailable"
        return result
    env = dict(os.environ)
    actual_bus = f"unix:path={bus}"
    result["inheritedBusDiffersFromUserManager"] = env.get("DBUS_SESSION_BUS_ADDRESS") != actual_bus
    env["DBUS_SESSION_BUS_ADDRESS"] = actual_bus
    env["XDG_RUNTIME_DIR"] = str(runtime)
    manager = run(["systemctl", "--user", "--no-ask-password", "show", "-p", "Version", "--value"], env)
    result["manager"] = manager
    if manager["code"] != 0:
        result["reason"] = "real user-manager connection failed"
        return result
    unit = f"e67-runtime-probe-{uuid.uuid4().hex}.service"
    description = f"E67 facility probe {unit}"
    attempted = False
    created = False
    pidfd = None
    with tempfile.TemporaryDirectory(prefix="e67-runtime-probe-") as temporary:
        try:
            # Child deliberately changes session: killpg(root) is not a tree owner.
            command = 'printf "%s" "$$" > "$E67_PROBE_DIR/root"; setsid sleep 60 & printf "%s" "$!" > "$E67_PROBE_DIR/child"; exit 0'
            attempted = True
            start = run(["systemd-run", "--user", "--quiet", "--collect", f"--unit={unit}", f"--description={description}",
                         "--property=Type=exec", "--property=RemainAfterExit=yes",
                         "--property=KillMode=control-group", "--property=TimeoutStopSec=3s",
                         f"--setenv=E67_PROBE_DIR={temporary}", "/bin/sh", "-c", command], env)
            result["start"] = start
            if start["code"] != 0:
                result["reason"] = "unprivileged transient service creation failed"
                return result
            created = True
            deadline = time.monotonic() + 3
            child_file = Path(temporary) / "child"
            while not child_file.exists() and time.monotonic() < deadline:
                time.sleep(0.02)
            child = int(child_file.read_text())
            pidfd = os.pidfd_open(child)
            poll = select.poll()
            poll.register(pidfd, select.POLLIN)
            control = run(["systemctl", "--user", "show", unit, "-p", "ControlGroup", "--value"], env)
            if control["code"] or not control["stdout"].startswith("/") or not control["stdout"].endswith(unit):
                raise RuntimeError("owned transient control group not established")
            group = Path("/sys/fs/cgroup") / control["stdout"].lstrip("/")
            child_group = Path(f"/proc/{child}/cgroup").read_text()
            result["childPID"] = child
            deadline = time.monotonic() + 3
            while os.getsid(child) != child and time.monotonic() < deadline:
                time.sleep(0.01)
            result["childDifferentSession"] = os.getsid(child) == child
            child_stat = Path(f"/proc/{child}/stat").read_text()
            result["childStartedTicks"] = int(child_stat[child_stat.rfind(")") + 1:].split()[19])
            result["childInOwnedCgroup"] = f"0::{control['stdout']}\n" in child_group
            result["rootStateBeforeStop"] = run(["systemctl", "--user", "show", unit,
                                                "-p", "MainPID", "-p", "ActiveState", "-p", "ExecMainStatus"], env)
            result["rootExitedBeforeStop"] = "MainPID=0" in result["rootStateBeforeStop"]["stdout"].splitlines()
            result["cgroupBeforeStop"] = (group / "cgroup.events").read_text()
            result["childInstanceLiveBeforeStop"] = not poll.poll(0)
            stop = run(["systemctl", "--user", "--no-ask-password", "stop", unit], env, timeout=6)
            result["stop"] = stop
            result["heldChildInstanceExited"] = bool(poll.poll(3000))
            result["cgroupRemoved"] = not group.exists()
            result["cgroupAfterStop"] = None if not group.exists() else (group / "cgroup.events").read_text()
            empty = result["cgroupRemoved"] or "populated 0" in result["cgroupAfterStop"]
            result["cleanupComplete"] = stop["code"] == 0 and result["heldChildInstanceExited"] and empty
            result["qualified"] = (result["cleanupComplete"] and result["childDifferentSession"]
                                    and result["rootExitedBeforeStop"] and result["childInOwnedCgroup"]
                                    and result["childInstanceLiveBeforeStop"])
        except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
            result["reason"] = str(error)
            result["cleanupComplete"] = False
        finally:
            if pidfd is not None:
                os.close(pidfd)
            # A timed-out launcher may nevertheless have created its unit.
            # Reconcile only our random description, never adopt another unit.
            if attempted and not created:
                try:
                    identity = run(["systemctl", "--user", "show", unit, "-p", "Description", "--value"], env)
                    created = identity["code"] == 0 and identity["stdout"] == description
                    result["lateUnitReconciled"] = created
                except subprocess.TimeoutExpired:
                    result["cleanupComplete"] = False
            if created:
                try:
                    cleanup = run(["systemctl", "--user", "--no-ask-password", "stop", unit], env, timeout=6)
                except subprocess.TimeoutExpired:
                    cleanup = {"code": -1, "stderr": "owned unit normal stop timed out"}
                result["finalStop"] = cleanup
                # Do not disguise a failed normal barrier as successful cleanup.
                if cleanup["code"] != 0 and "not loaded" not in cleanup["stderr"]:
                    result["cleanupComplete"] = False
                    result["qualified"] = False
    return result


parser = argparse.ArgumentParser()
parser.add_argument("--xres-probe", required=True)
parser.add_argument("--output", required=True)
args = parser.parse_args()
try:
    systemd = systemd_probe()
except (OSError, subprocess.TimeoutExpired) as error:
    systemd = {"qualified": False, "cleanupComplete": True, "reason": str(error)}
report = {"kernel": os.uname().release, "uid": os.getuid(),
          "sessionType": os.environ.get("XDG_SESSION_TYPE"), "systemd": systemd}
try:
    report["xres"] = run([str(Path(args.xres_probe).resolve())])
except (OSError, subprocess.TimeoutExpired) as error:
    report["xres"] = {"code": -1, "stderr": str(error), "stdout": ""}
try:
    report["xres"]["evidence"] = json.loads(report["xres"]["stdout"])
except ValueError:
    report["xres"]["evidence"] = {"qualified": False}
output = Path(args.output)
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps(report, indent=2) + "\n")
print(json.dumps(report, indent=2))
# Missing capability is evidence, not a green product gate or an instruction
# to elevate. A cleanup failure does fail the probe job.
raise SystemExit(0 if report["systemd"]["cleanupComplete"] else 1)
