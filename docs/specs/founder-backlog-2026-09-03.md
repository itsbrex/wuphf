# Founder backlog — opened 2026-09-03

Running list of issues raised by the founder in session. Every item gets a
disposition: OPEN, IN PROGRESS, FIXED (with commit), or DEFERRED (with reason).

Working branch: `fix/bot-rename-and-blocking-ui` (worktree
`~/.wuphf-botrename`, based on `origin/main`).

Landing rule for this repo: no PRs, push straight to `main`. Hooks and CI are
the controls. Never `--no-verify`.

---

## B1 — Blocking question is not in the chat

**Status:** FIXED — commit 797c679f5

**Reported:** "the blocking question is not in the chat. it should be. also
remove 'Office' entity from here. this should not be a text but the UI should
do the work"

**Observed:** In DM `#ceo__human`, asking "can you spin up a prospecting agent"
produces a prose system message:

> **Office** 23:58
> ❓ @ceo asks you (blocking) (request request-3): Add Prospector (@prospector,
> Lead Generation & Prospecting Specialist) to the team? Answer it in the
> Inbox, or reply in this thread.

Three defects in one:

1. The ask renders as **prose**, not an interactive card. The human has to read
   a sentence and go somewhere else, instead of clicking Approve/Deny in place.
2. It is authored by a fake **"Office"** entity. Source is
   `From: "system"` in `postRequestRaisedChatMessageLocked`
   (`internal/team/broker_requests_interviews.go:1240`). The real asker is
   `@ceo`.
3. It points to **"the Inbox"**, which is no longer a nav destination — Inbox
   was consolidated into Tasks (see
   `project_consolidate_inbox_into_tasks_2026_06_14`). Dead pointer.

**Source:** `internal/team/broker_requests_interviews.go:1238-1252`

**Fix direction:** the raised request should render as an inline interactive
card in the message list at its timestamp position, attributed to the asking
bot, with the options as real buttons that resolve the request in place.
`InterviewBar` already renders options interactively but is docked above the
composer, not in-thread.

---

## B2 — Tasks badge/notification fires with an empty task list

**Status:** FIXED — commit d890b9c2b

**Reported:** "it says there is an active task that needs me and even
notification and notification sound said that but no tasks are visible"

**Observed:** Tasks page shows chips `1 NEED YOU` and `1 ACTIVE`, sidebar Tasks
badge shows `1`, a desktop notification and sound fired — but the board body
renders the empty state: "No tasks yet. The team is watching this space…"

**Root cause (confirmed):**
`web/src/components/lifecycle/TasksList.tsx:750`

```tsx
if (tasks.length === 0) {
  return <TasksEmptyState … />;   // early return
}
```

The gate counts only lifecycle **tasks**. But the Needs-human lane also folds
in **attention items** — blocking requests and pending reviews — computed just
above at line 695 (`attentionItems`) and rendered at line 885. With zero tasks
and one blocking request, the component returns the empty state before those
attention items ever render.

Meanwhile the chips read `needsYouCount(stats)` =
`tasks.needs_human + requests.blocking` (`web/src/lib/needsYou.ts:64`), which
correctly counts the blocking request. So the header counts 1 and the body
shows 0.

This is the **same underlying request** as B1: the blocking approval has no
visible home on either surface.

**Fix direction:** gate the empty state on `tasks.length === 0 &&
attentionItems.length === 0`. Needs a regression test that fails pre-fix:
zero tasks + one blocking request must render the card, not the empty state.

---

## B3 — Rename "Agent" → "Bot"

**Status:** IN PROGRESS

**Reported:** "rename all 'Agent' stuff to be called 'bot'"

**Scope decided by founder:** surface + code, **wire stable**.

Rename:
- UI copy and labels ("Agents" → "Bots", "New Agent" → "New Bot")
- Go identifiers (`AgentConfig` → `BotConfig`, `agentSlug` → `botSlug`)
- TS/TSX identifiers and filenames (`AgentPanel.tsx` → `BotPanel.tsx`)
- docs, prompts, website copy

Do **not** rename (compatibility — would break every existing install):
- JSON wire keys: `agent_slug`, `agent_id`, `agents`, `one_on_one_agent`, …
- HTTP routes: `/api/agents/`, `/agent-stream/`, `/agent-logs`
- MCP tool names exposed to models: `agent_message`, `agent_slug`, …
- on-disk state: `~/.wuphf/agents/<slug>/…`

Also do **not** rename (not the product concept):
- `AGENTS.md` files — the AI-coding-agent instruction convention (Claude/Codex
  read these by name)
- `.claude/agents/`, `docs/agents/`
- `User-Agent` HTTP headers
- `packages/agent-runners` — external runner integration naming
- prose about third-party AI coding agents in `CLAUDE.md` / `AGENTS.md`

**Measured scope:** 16,527 occurrences across 1,084 files (main tree, excluding
worktrees, `.gomodcache`, `node_modules`, `web/dist`).

**Landing:** one mechanical commit straight to `main`, after the B1/B2 fixes,
so the ~40 in-flight worktrees rebase onto it once.

---

## B4 — Chief of Staff first-response latency

**Status:** FIXED (primary cause) — commit a2e605d84

**Reported:** "chief of staff took at least 1 min to answer my first question.
should have taken less than 2 secs. why the lag?"

**Observed:** After the founder's first message in `#ceo__human`, Chief of Staff
sat in `is typing · Working…` for ~1 minute before producing anything. Status
bar showed `running mcp__wuphf-office…`.

**Target:** first visible token in under 2s.

**Not yet diagnosed.** Candidates to rule out, cheapest first:
- cold spawn of the `claude-code` provider process per turn
- prompt-build cost before the first model call (wiki/gbrain retrieval,
  embeddings, context packing)
- the "first tool call MUST be `team_task action=create`" rule in
  `prompt_builder.go` forcing a full MCP round-trip before any user-visible
  output
- serialized MCP handshake / tool-list fetch on every turn
- no streaming — output withheld until the turn completes

---

## Disposition log

| # | Item | Status | Notes |
|---|------|--------|-------|
| B1 | Blocking question not in chat | FIXED | 797c679f5 — interactive in-thread card, "Office" gone |
| B2 | Tasks badge vs empty list | FIXED | d890b9c2b — empty state now gated on attention items too |
| B3 | Agent → Bot rename | IN PROGRESS | phase A tooling built + validated |
| B4 | Chief of Staff latency | FIXED | a2e605d84 — token deltas; follow-ups B4a/B4b below |
| B5 | New Agent wizard unreadable | FIXED | 04802c7ef — 1.26:1 → 12.00:1 / 15.60:1, verified in browser |
| B6 | Dev script ignores PORT_WEB | OPEN | scripts/dev-mvp.sh only gates its health check |


---

## B5 — New Agent wizard unreadable on dark themes

**Status:** FIXED — commit 04802c7ef

**Reported:** "fix new agent flow UI. it is unreadable"

**Observed:** In the Create-agent modal, the "Create agent" heading, every
field label, and every placeholder rendered near-black on a near-black card.
Only the help paragraphs and the tab labels were legible.

**Root cause:** `nex-shell.css` and `nex-dark.css` both
`@import url("./nex.css")` as their base, and `nex.css` scopes its sidebar
rules with `[data-theme^="nex"]` — a **prefix** match, so those rules also
apply under `nex-shell` and `nex-dark`.

The sidebar remaps the text ramp to white-on-brand for its rail. A companion
rule resets that ramp inside dialogs anchored under the sidebar — but it reset
to a hardcoded LIGHT palette (`--neutral-900` ink), on the assumption its own
comment stated: "invisible on the modal's white card". Under `nex-shell` the
card is `hsl(210 5% 10%)`, so the reset painted `#28292a` on `#131416`.

**Fix:** capture the active theme's ramp on `<html>` (where `--text` still
holds the theme's own value) into `--text-*-root`, and restore from those
instead of a hardcoded palette. The capture must live on an ancestor that
never overrides `--text`.

**Verified in gstack browser**, title/label contrast vs the modal card:

| Theme | Before | After |
|---|---|---|
| nex-shell | 1.26:1 | **12.00:1** |
| nex-dark | 1.26:1 | **15.60:1** |
| nex (light) | 14.57:1 | 14.57:1 (unchanged) |

---

## B4a — First assistant message is always a tool call

**Status:** OPEN (follow-up to B4)

`prompt_builder.go:653` RULE ZERO: "your FIRST tool call MUST be team_task
action=create". So the first assistant message carries no prose at all — even
with token streaming, the human waits a full tool round-trip for the first
word. Worth letting the bot emit a one-line acknowledgement before the tool
call.

---

## B4b — Cold subprocess + MCP handshake every turn

**Status:** OPEN (follow-up to B4)

Each turn spawns the `claude` CLI fresh (`exec.CommandContext` in
`internal/provider/claude.go`) and hands it `--mcp-config`
(`internal/team/headless_claude.go:50`), so the office MCP server is launched
and its tool list negotiated before the first inference token. A warm pool or
a persistent MCP connection would cut the floor further.

---

## B6 — scripts/dev-mvp.sh ignores PORT_WEB

**Status:** OPEN

`PORT_WEB` only gates the script's pre-kill and health check; the binary is
started without `--web-port`, so it always binds the default 7890/7891.
Running the script with `PORT_WEB=7899` therefore reported "broker failed to
bind :7899" while the broker had in fact taken 7890/7891 — the ports a
developer's real office is on. Pass the port through to the binary, or drop
the variable.

Hit live on 2026-09-03: it displaced the founder's running office.
