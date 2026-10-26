#!/usr/bin/env python3
# SPDX-License-Identifier: LGPL-3.0-or-later
"""Record the terminal sessions and the sind ui tour of the overview video.

Runs the scenario of the video for real, on a host with Docker: every
command runs in an interactive bash in a pseudo-terminal of COLS x ROWS,
so sind draws its progress tree and its colours as it does for a user.
What the commands print is kept with its timing in terminal.json; the
compositor types each input line and plays the output.

The host needs Docker, the sind to record first on PATH, mise, jq, and
host DNS for *.sind (systemd-resolved and the polkit rule that sind doctor
prints). The scenario creates clusters in the realms sind and ci and
deletes them at the end.

    record.py --out recordings [--only SHOT ...]
"""
import argparse
import codecs
import fcntl
import json
import os
import pty
import re
import select
import shutil
import signal
import struct
import subprocess
import sys
import termios
import time

COLS, ROWS = 100, 26
HERE = os.path.dirname(os.path.abspath(__file__))
WORK = os.path.expanduser("~/sind-demo")
ANSI = re.compile(r"\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[=>]")
HOST = r"~/sind-demo \$ $"  # the host prompt, as PS1 below draws it
NODE = r"# $"  # a root shell on a node
PS1 = r"\[\e[38;5;37m\]~/sind-demo\[\e[0m\] \[\e[38;5;244m\]$\[\e[0m\] "


def plain(s):
    return ANSI.sub("", s).replace("\r", "")


class Session:
    """An interactive bash in a pseudo-terminal whose output is recorded.

    Inputs are sent with the terminal's echo off, so the output holds only
    what the programs print; the compositor types the inputs itself.
    """

    def __init__(self, shot, env):
        self.shot = shot
        pid, fd = pty.fork()
        if pid == 0:
            os.chdir(WORK)
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
            os.execvpe("bash", ["bash", "--noprofile", "--norc", "--noediting", "-i"], env)
        self.pid, self.fd = pid, fd
        self.dec = codecs.getincrementaldecoder("utf-8")("replace")
        self.t0 = time.monotonic()
        self.events = []
        self.recording = False
        self.pending = ""
        self.prompt = ""
        # Set up the shell before anything is recorded.
        self.expect(r"[$#] $", 30)
        self.hidden(f"stty -echo; PS1='{PS1}'; PROMPT_COMMAND=", HOST)

    def now(self):
        return round(time.monotonic() - self.t0, 3)

    def emit(self, kind, data=None, **kw):
        ev = {"t": self.now(), "k": kind}
        if data is not None:
            ev["d"] = data
        ev.update(kw)
        self.events.append(ev)

    def expect(self, pattern, timeout):
        """Read until the output since the last input ends with pattern."""
        rx = re.compile(pattern)
        seen = ""
        end = time.monotonic() + timeout
        while True:
            left = end - time.monotonic()
            if left <= 0:
                raise TimeoutError(f"{self.shot}: no {pattern!r} within {timeout}s; got {plain(seen)[-400:]!r}")
            r, _, _ = select.select([self.fd], [], [], min(left, 0.5))
            if not r:
                continue
            try:
                data = os.read(self.fd, 65536)
            except OSError:
                raise EOFError(f"{self.shot}: the shell ended")
            s = self.dec.decode(data)
            if not s:
                continue
            seen += s
            if self.recording:
                self.emit("o", s)
            else:
                self.pending += s
            if rx.search(plain(seen)):
                return seen

    def send(self, line, wait=HOST, timeout=120):
        """Type a line and wait for the next prompt."""
        self.emit("i", line)
        os.write(self.fd, (line + "\n").encode())
        return self.expect(wait, timeout)

    def hidden(self, line, wait=HOST, timeout=120):
        """Run a line that the video does not show, such as a wait.

        Its output is dropped. With the echo off, the prompt that the shell
        prints after it lands where the cursor already is, so the screen of
        the video keeps the prompt it has, and the next input follows it.
        """
        was, self.recording = self.recording, False
        self.pending = ""
        os.write(self.fd, (line + "\n").encode())
        out = self.expect(wait, timeout)
        self.recording = was
        self.prompt = self.pending.rsplit("\n", 1)[-1]
        self.pending = ""
        return out

    def key(self, ch, label, wait=HOST, timeout=30):
        """Press a key such as Ctrl-C, and wait for the prompt."""
        self.emit("key", label)
        os.write(self.fd, ch.encode())
        return self.expect(wait, timeout)

    def pause(self, seconds):
        """Let a running command print for a while."""
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            r, _, _ = select.select([self.fd], [], [], end - time.monotonic())
            if r:
                s = self.dec.decode(os.read(self.fd, 65536))
                if s and self.recording:
                    self.emit("o", s)

    def start(self):
        """Start recording: the video shows the prompt from here on."""
        self.t0 = time.monotonic()
        self.recording = True
        self.emit("o", self.prompt)

    def mark(self, name):
        self.emit("mark", name)

    def close(self):
        # A shell without line editing prints "exit" when it exits; a user's
        # terminal shows only the typed word.
        for i, ev in enumerate(self.events[:-1]):
            nxt = self.events[i + 1]
            if ev["k"] == "i" and ev["d"] == "exit" and nxt["k"] == "o" and nxt["d"].startswith("exit\r\n"):
                nxt["d"] = nxt["d"][len("exit\r\n"):]
        try:
            os.write(self.fd, b"exit\n")
            os.waitpid(self.pid, 0)
        except OSError:
            pass
        os.close(self.fd)
        return {"id": self.shot, "events": self.events}


# ---------------------------------------------------------------- shots
# Each shot is one terminal window of the video. The names match the acts
# of the compositor (compositor/acts.js).

def shot_install(s):
    s.send("mise use -g github:GSI-HPC/sind", timeout=300)
    s.send("sind version")


def shot_doctor(s):
    s.send("sind doctor")


def shot_create(s):
    s.send("sind create cluster --config dev.yaml", timeout=900)
    s.send("sind get clusters")
    s.send("sind get nodes dev")


def shot_slurm(s):
    s.send("sind enter dev", wait=NODE)
    s.hidden("set +o emacs +o vi; stty -echo; PROMPT_COMMAND=", NODE)
    s.send("sinfo", wait=NODE)
    s.send("ls", wait=NODE)
    s.send("sbatch --array=1-8 job.sh", wait=NODE)
    s.hidden("sleep 3", NODE)
    s.send("squeue", wait=NODE)
    s.mark("wait-jobs")
    s.hidden('until [ -z "$(squeue -h)" ]; do sleep 2; done', NODE, timeout=300)
    s.send("sacct -X --format=JobID,JobName,State,NodeList", wait=NODE)
    s.send("exit")
    s.send("sind exec dev -- srun -N4 hostname")


def shot_ssh(s):
    s.send("sind ssh worker-2.dev", wait=NODE)
    s.hidden("set +o emacs +o vi; stty -echo; PROMPT_COMMAND=", NODE)
    s.send("hostname", wait=NODE)
    s.send("systemctl is-active slurmd munge", wait=NODE)
    s.send("exit")


def shot_sshconfig(s):
    s.send("sind get ssh-config")
    s.send("echo 'Include ~/.local/state/sind/*/ssh_config' >> ~/.ssh/config")
    s.send("ssh controller.dev sinfo --summarize")
    s.send("scp results.csv worker-0.dev:/tmp/")


def shot_forward(s):
    s.hidden("TOKEN=$(sind exec dev -- scontrol token username=root lifespan=900 | cut -d= -f2)")
    s.send("ssh -fN -L 8080:localhost:6820 api.dev")
    s.send('curl -s -H "X-SLURM-USER-NAME: root" -H "X-SLURM-USER-TOKEN: $TOKEN" '
           "localhost:8080/slurm/v0.0.45/ping/ | jq -c '.pings[]'")


def shot_dns(s):
    s.send("resolvectl query worker-0.dev.sind.sind")
    s.send("ping -c 2 controller.dev.sind.sind")


def shot_logs(s):
    s.emit("i", "sind logs worker-0.dev slurmd --follow")
    os.write(s.fd, b"sind logs worker-0.dev slurmd --follow\n")
    s.pause(5)
    s.key("\x03", "^C")


def shot_workers(s):
    s.send("sind create worker dev --count 2", timeout=600)
    s.send("sind get nodes dev")


def shot_power(s):
    s.send("sind power freeze worker-2.dev")
    s.mark("wait-down")
    s.hidden("until sind exec dev -- sinfo -h -n worker-2 -o %t | grep -q '[*]'; do sleep 2; done", timeout=180)
    s.send("sind exec dev -- sinfo")
    s.send("sind power unfreeze worker-2.dev")
    s.send("sind power reboot 'worker-[0-3].dev'", timeout=600)


def shot_delworkers(s):
    s.send("sind delete worker 'worker-[4-5].dev'", timeout=300)


def shot_rest(s):
    s.send("TOKEN=$(sind exec dev -- scontrol token username=root | cut -d= -f2)")
    s.send('curl -s -H "X-SLURM-USER-NAME: root" -H "X-SLURM-USER-TOKEN: $TOKEN" '
           "http://api.dev.sind.sind:6820/slurm/v0.0.45/nodes/ | jq -r '.nodes[] | [.name, .state[0], .cpus] | @tsv'")


def shot_users(s):
    s.send("sind exec dev -u alice -- sbatch --wrap 'sleep 30'")
    s.send("sind exec dev -- squeue -o '%.6i %.8u %.10a %.8T %N'")
    s.send("sind exec dev -- sacctmgr -n show assoc format=account,user")


def shot_failover(s):
    s.send("sind exec ha -- scontrol ping")
    s.send("sind power cut controller.ha")
    s.mark("wait-takeover")
    s.hidden("until sind get cluster ha | grep -q 'backup[*]'; do sleep 2; done", timeout=180)
    s.send("sind get cluster ha")


def shot_monitoring(s):
    s.send("sind get monitoring")


def shot_realm(s):
    s.send("SIND_REALM=ci sind create cluster", timeout=900)
    s.send("SIND_REALM=ci sind get clusters")
    s.send("sind get clusters")


def shot_connect(s):
    # A connected container reaches the nodes by address; resolving their
    # names needs a forward to the realm's DNS (see the kind guide).
    s.send("docker run -d --name app alpine:3 sleep 1d", timeout=300)
    s.send("sind connect container app")
    s.send("docker inspect app -f '{{range $n, $_ := .NetworkSettings.Networks}}{{$n}} {{end}}'")
    s.send("docker exec app ping -c 2 $(getent hosts controller.dev.sind.sind | cut -d' ' -f1)")


def shot_plain(s):
    s.send("SIND_REALM=ci sind create cluster lab --progress plain --progress-log lab.jsonl", timeout=600)
    s.send("""jq -c 'select(.type == "end" and .kind == "step") | {name, status}' lab.jsonl""")


def shot_delete(s):
    s.send("sind delete cluster --all", timeout=600)


# The order is the order of the recording; the compositor orders the acts.
SHOTS = [
    ("install", shot_install), ("doctor", shot_doctor), ("create", shot_create), ("slurm", shot_slurm),
    ("ssh", shot_ssh), ("sshconfig", shot_sshconfig), ("dns", shot_dns), ("forward", shot_forward),
    ("logs", shot_logs), ("workers", shot_workers), ("power", shot_power), ("delworkers", shot_delworkers),
    ("rest", shot_rest), ("users", shot_users),
    ("@ha", None), ("failover", shot_failover), ("monitoring", shot_monitoring),
    ("realm", shot_realm), ("connect", shot_connect), ("plain", shot_plain),
    ("@bench", None), ("@ui", None), ("delete", shot_delete),
]


def sh(cmd, env, timeout=900, check=True):
    print(f"+ {cmd}", flush=True)
    return subprocess.run(["bash", "-c", cmd], cwd=WORK, env=env, timeout=timeout, check=check)


def record_ui(out, env):
    """Start sind ui as a user would, and record the tour of record_ui.mjs."""
    s = Session("ui", env)
    s.start()
    s.emit("i", "sind ui --no-open")
    os.write(s.fd, b"sind ui --no-open\n")
    seen = s.expect(r"Press Ctrl-C to stop\.", 30)
    url = re.search(r"http://127\.0\.0\.1:\d+/\?token=\S+", plain(seen)).group(0)
    s.pause(1)
    shot = {"id": "ui", "events": list(s.events)}  # up to the URL, not the Ctrl-C
    subprocess.run(["node", os.path.join(HERE, "record_ui.mjs"), url, os.path.join(out, "ui")], env=env, check=True)
    os.write(s.fd, b"\x03")
    s.expect(HOST, 30)
    s.close()
    return shot


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True)
    ap.add_argument("--only", nargs="*", help="record only these shots (for a try on a host that has their clusters)")
    args = ap.parse_args()
    out = os.path.abspath(args.out)
    os.makedirs(out, exist_ok=True)
    os.makedirs(WORK, exist_ok=True)
    for f in os.listdir(os.path.join(HERE, "scenario")):
        shutil.copy(os.path.join(HERE, "scenario", f), WORK)
    with open(os.path.join(WORK, "results.csv"), "w") as f:
        f.write("task,seconds\n1,20.1\n2,20.0\n")
    os.makedirs(os.path.expanduser("~/.ssh"), mode=0o700, exist_ok=True)

    env = dict(os.environ, TERM="xterm-256color", COLORTERM="truecolor", LANG="C.UTF-8", LC_ALL="C.UTF-8")
    env.pop("SIND_PROGRESS", None)
    env.pop("NO_COLOR", None)
    env.pop("SIND_REALM", None)
    mise_env = dict(env, PATH=os.path.expanduser("~/.local/share/mise/shims") + ":" + env["PATH"])

    result = {"cols": COLS, "rows": ROWS, "shots": []}
    path = os.path.join(out, "terminal.json")
    for name, fn in SHOTS:
        if args.only and name.lstrip("@") not in args.only:
            continue
        print(f"== {name}", flush=True)
        if name == "@ha":
            sh("sind create cluster --config ha.yaml", env)
        elif name == "@bench":
            # A run that fails, for the web UI: the image does not exist.
            sh("sind create cluster --config bench.yaml", env, check=False)
        elif name == "@ui":
            result["shots"].append(record_ui(out, env))
        else:
            s = Session(name, mise_env if name == "install" else env)
            s.start()
            fn(s)
            s.pause(0.5)
            result["shots"].append(s.close())
        with open(path, "w") as f:
            json.dump(result, f)
    # What the delete shot leaves: the ci realm and the connected container.
    sh("SIND_REALM=ci sind delete cluster --all", env, check=False)
    sh("docker rm -f app", env, check=False)
    print(f"wrote {path}")


if __name__ == "__main__":
    signal.signal(signal.SIGINT, signal.default_int_handler)
    sys.exit(main())
