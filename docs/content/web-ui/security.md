---
weight: 344
title: "Security and Remote Use"
description: "How sind ui keeps others out, and how to use it from another machine"
toc: true
---

## Security

The UI can do what your Docker access can do, so sind protects it in these ways:

- **Only your user.** The UI refuses connections from processes of other users before it reads any request. This works on Linux only.
- **A token in the URL.** Each run prints a new token. The first visit with the token sets a cookie for this browser, and the address bar then shows the page without the token. A visit with an old or wrong token, or without the cookie, is refused with a page that tells how to get the address. It never shows the token.
- **Your own pages only.** The UI accepts requests for `127.0.0.1` only, not for `localhost`. A request that changes something must come from the UI itself, with its CSRF token. A web page in another tab cannot start an action. A page that another program serves on another port of `127.0.0.1` cannot read the UI or its event and log streams.
- **No secrets.** The UI has no page for the munge key, the JWT key or the SSH private key.
- **No outside resources.** The UI loads its scripts, styles and fonts from itself only. No other site can show it in a frame.

## Use it from another machine

The UI does not accept connections from other hosts. To use it from a laptop, start `sind ui --no-open` on the host and forward its port over SSH:

```bash
# on the laptop
ssh -L 41873:127.0.0.1:41873 user@host
```

Use the same port on both ends. The UI checks the host name that the browser sends, so a different local port does not work. Then open the URL that the host printed, `http://127.0.0.1:41873/?token=...`, on the laptop.
