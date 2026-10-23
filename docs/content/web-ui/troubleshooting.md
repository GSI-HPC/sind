---
weight: 345
title: "Troubleshooting"
description: "Fixes for the problems of sind ui"
toc: true
---

## Port in use

```
sind ui: port 8080 is in use; pick another with --port, or leave it out
```

Another program listens on that port. Run `sind ui --port 8081`, or leave `--port` out so that sind takes a free port.

## A second sind ui opens the first

```
sind ui is already running: http://127.0.0.1:41873/?token=...
```

sind ui recorded the running UI in `$XDG_RUNTIME_DIR/sind/ui.json`. The second command opens that UI in your browser and exits. If the browser does not open, copy the URL into it. Normally only one UI runs for your user at a time.

To start a new one, press Ctrl-C in the terminal of the running UI. If the UI is gone but the record is left over, the next `sind ui` starts a new one.

If `XDG_RUNTIME_DIR` is not set, sind ui keeps no record, so each `sind ui` starts its own UI on its own port.

## Docker is not reachable

The Overview shows **Docker is not reachable**. The pages show the data that sind ui read last. Open **Doctor** for the cause and the fix. Or run `sind doctor` in a terminal, as described in [Diagnostics]({{< relref "/usage/diagnostics" >}}).

## The page says the link is old or wrong

The token is new with each run. Use the URL that the running `sind ui` printed, or the one that the second command printed. A bookmark of an older run does not work. A tab of an older run that is still open is refused too, as its session is gone. Open the new URL.

## Opened by another host name

```
sind ui answers only to 127.0.0.1:41873
```

The browser sent another host name, so the UI refused the request. Open the URL that sind printed, with `127.0.0.1` and the same port. A URL with `localhost` does not work: a browser can send it to `::1`, where sind ui does not listen.

## The browser does not open

sind ui does not open a browser in an SSH session, or on Linux without a display (`DISPLAY` or `WAYLAND_DISPLAY`). It prints the URL instead: copy it into a browser on this machine, or forward the port as described in [Use it from another machine]({{< relref "/web-ui/security#use-it-from-another-machine" >}}). With `--no-open` it never opens one.

## A run says "no end recorded"

The activity log has no end for this run, and its last event is more than an hour old. Its command was most likely killed, for example with `kill -9`, so it could not write its end. Check with `sind get cluster` what the command left, and delete it if needed. A run that sind ui started ends when its command ends, with the outcome of the command.

## A command waits for the realm lock

The Overview says that a command holds the lock of the realm, and it names that command. Your command goes on when the other one ends. Open **Activity** to see the runs. If the other command hangs, stop it: press **Stop** if the UI started it, or press Ctrl-C in its terminal. See [Realms]({{< relref "/configuration/realms" >}}) for the lock.
