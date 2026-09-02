# Every bot gets a computer

A gawkbot can have its own Linux desktop: a browser, a terminal, files, and a
screen you can watch while it works. The bot's brain stays on your machine
(the `claude` or `codex` CLI you already use); its hands live in one of
three places you pick per bot in the Computer tab.

| Runs on | What it is | Cost |
|---|---|---|
| Local VM | A hardened container on this machine, built from a pinned Cua desktop image | Free |
| Cloud | A persistent VM from ascii.dev Box, reached through their API | Paid, bring your own key |
| Off | No hands. The bot chats and uses connected apps only | Free |

The default is Local VM whenever a container runtime is running, and Off
otherwise. Nothing is created until a bot first needs its computer during a
turn, and idle computers sleep after ten minutes.

## Local VM

You need one of:

- OrbStack or Docker Desktop (macOS, Windows, Linux)
- Apple's `container` CLI on macOS 26
- Podman

Open any bot's Computer tab. The first time, choose **Prepare the desktop
image**. This pulls the pinned Cua XFCE base image (about 1.3 GB) and builds
a small layer that installs Cua Driver 0.20.0 from a SHA-256 verified wheel.
It takes a couple of minutes once, and every bot shares the image.

Each bot then gets its own container:

- 2 GB of memory, 2 CPUs, 512 processes
- every Linux capability dropped, private IPC and cgroup namespaces
- no host folders except the bot's own workspace, mounted at
  `/home/cua/workspace`
- the only published port is the live viewer, on 127.0.0.1

Before every attach gawkbot inspects the container and refuses one that
fails any of those rules, including one someone created under the managed
name. Refuse, never repair.

Only `/home/cua/workspace` survives a recreate. Browser profiles live there,
so logins survive too.

## Cloud

Bring your own ascii.dev account. Two ways in, both during onboarding or
later in Settings:

- **Sign in to ascii.dev.** gawkbot installs the Box CLI (one static binary
  under `~/.ascii/bin`), opens ascii.dev's GitHub, Google, or email sign-in
  in your browser, mints an API key named `gawkbot` on your account, checks
  it, and saves it. Nothing to copy.
- **Paste a key.** Create one at box.ascii.dev under API keys (they start
  with `box_`). gawkbot checks the key with ascii.dev before saving it.

A saved key is not the whole story: ascii.dev only starts boxes for an
account with a plan or an active 7-day trial. Wherever the cloud shows up
(onboarding, the Computer tab, Settings) gawkbot reads the account's limits
and, when boxes are blocked, says so and links to the billing page. Settings
→ API Keys also has **Sign out and sign in again**, which revokes the
`gawkbot` key on your account, ends the CLI session, and forgets the key.

Then choose **Cloud** for the bot. The first turn creates a box named after
the bot, installs the driver, and gives the bot the same tools. You pay
ascii.dev directly; gawkbot never sees a bill. Sleep archives the box;
billing pauses and the disk survives. Take control opens the provider's
desktop link.

## Watching and taking control

The Computer tab shows the bot's screen live while it works, and a frame
appears in the thread after every turn that touched the screen.

**Take control** pauses the bot's hands and hands you the desktop. While you
hold it, every click and keystroke the bot sends is refused, not queued, so
nothing lands after you have moved on. **Hand control back** lets the bot
continue; it is told to take a fresh screenshot first.

A bot can ask for your hands with `computer_request_help`, for a login, a
CAPTCHA, or a judgment call. The tab shows the request; you take control,
finish the step, and hand it back.

## For the bot

The bot sees one MCP server named `computer` (tools `mcp__computer__*`)
provided by Cua Driver inside its machine: screenshots, the accessibility
tree, clicks, typing, hotkeys, scrolling, window and app management, plus
`computer_request_help`. On a cloud box the same names are served by
gawkbot's REST proxy, where each action returns the resulting screenshot in
the same call.

## Where things live

```
~/.wuphf/computers/<sha16>/     each bot's durable workspace
gawkbot-computer-<sha16>        the container name (a digest of the slug)
localhost/gawkbot/cua-computer  the shared image
```

Environment overrides: `WUPHF_COMPUTERS_DIR`, `WUPHF_BOX_API_KEY`,
`WUPHF_BOX_API`.
