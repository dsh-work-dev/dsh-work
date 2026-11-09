# Linux runtime facility probe

Run on the actual Linux runner before selecting a product supervisor dependency.
This probes a self-owned transient service and an unmapped GTK4 window; it does
not qualify a Wails presenter, CEF sandbox, or full Linux product.

Prerequisites: Python 3 with `os.pidfd_open`, systemd user clients, util-linux,
GTK4 development files, X11/XRes development files, and a local X11 server.
The CI dependency step can reuse the existing browser-lab packages plus
`libxres-dev`. No systemd/global policy configuration is changed by this probe.

```bash
mkdir -p "$RUNNER_TEMP/e67-linux-probe"
cc -Wall -Wextra -Werror tools/linux-runtime-probe/xres.c \
  -o "$RUNNER_TEMP/e67-linux-probe/xres" \
  $(pkg-config --cflags --libs gtk4 xres)
GDK_BACKEND=x11 dbus-run-session -- xvfb-run -a \
  python3 tools/linux-runtime-probe/probe.py \
  --xres-probe "$RUNNER_TEMP/e67-linux-probe/xres" \
  --output results/e67-linux-runtime/facilities.json
```

`dbus-run-session` supplies GTK's test session bus. The supervisor probe instead
connects explicitly to the already existing `$XDG_RUNTIME_DIR/bus`. A private
session bus is not evidence that a systemd user manager exists. Missing manager,
unprivileged unit creation, or cgroup facilities are recorded as unavailable;
the script does not start/configure a user manager, use sudo, or fall back to
killpg. Run the same command from an ordinary target desktop terminal to compare
the actual desktop facilities with CI.

Required observations for the proposed supervisor: an unprivileged transient
unit owns a live child in a different session, the child is in that unit's
cgroup, normal stop exits its held pidfd instance, and cgroup `populated` becomes
zero or the manager removes that exact cgroup. Cleanup failure fails the job;
missing capability is a decision input, not a passing product gate.

Required XRes observation: the unmapped but realized GTK4 XID resolves through
XRes 1.2 to this fixture's actual OS PID. `_NET_WM_PID` is never consulted. A
positive result establishes server API availability only; the next product
slice still needs the real Wails UI peer/creation/XID binding, presentation
readback, and foreign-window rejection.
