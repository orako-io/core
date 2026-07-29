# Orako — ask a human before you guess

> Drop-in `AGENTS.md` guidance for any agent that reads this convention (Codex,
> Cursor, Aider, Jules, Hermes-based loops, …). Paste this section into your
> project's `AGENTS.md`, or ship the file as-is. It carries the same policy as the
> Claude-Code `SKILL.md`; the transport detail (HTTP + JSON) is in
> [references/connect-json.md](../references/connect-json.md).

Orako connects you to your team's human experts and to every past answer. It is
**LLM-free**: it never generates answers — it *routes* your question to the right
person and *stores* what comes back, so a human is asked **once, not ten times**.

Reach for Orako at the exact moment you would otherwise **guess** on something
only a human can settle — product intent, the *why* behind a past decision, an
under-specified requirement, tribal knowledge that is not in the code, docs, or
tickets. A short async question is cheaper than a wrong PR caught in review — or
in prod. This matters most in **headless / autonomous runs** (CI, background
jobs, scheduled agents): no human is at the keyboard to stop you, so asking
through Orako is the only way to get a human decision instead of shipping a
confident wrong guess.

**Do NOT use it for things you can answer yourself.** Read the code, the docs, the
tickets, and your own tools first. Orako is the last resort *after* your own
sources come up empty — never a shortcut around reading the repo.

## The loop, in order

1. **`SearchHistory` FIRST — always, before asking a human.** Most questions were
   already asked once; searching is free and instant, asking a human costs their
   time. Search the clearest restatement of the question; if the first search is
   empty, try one rephrasing before concluding it is a miss.

2. **Branch on each hit's `status`** — this is how history routes you instead of
   asking again:
   - `resolved` → **reuse it.** `GetConversation` on the hit's `conversationId`
     for the full answer. (If it looks stale or disputed, ask fresh instead.)
   - `open` → the **same question is already in flight.** Do NOT open a duplicate —
     `AddParticipant` to join that thread and converge there.
   - `timed_out` / `dismissed` → asked but never answered. Re-ask the original
     `askerMemberId` via `Ask`.
   - **no hit** → ask a fresh question (steps 3+).

3. **On a genuine miss, pick who to ask.** `ListExperts` shows who is routable by
   `domains` (expertise tags). Target **one** expert by `memberId` when the owner
   is obvious; otherwise pass `domains` to reach every matching teammate — all
   replies land on one thread, the first answerer is recorded. Route on
   **expertise, never on presence** — anyone contacted can answer at any moment.
   Multi-project org? `ListProjects` and pick the project the question belongs to;
   never rely on silent auto-scoping.
   - **Self-ask:** your own user is a valid target. In a long autonomous run where
     the person with the answer is the very human you work for and they are away
     from the terminal, `Ask` THEM by their `memberId` — Orako reaches them on
     Discord/Slack and you pick up the reply with `GetConversation`. Prefer that
     over stalling.

4. **Assemble a self-contained context packet before asking** — files, symbols,
   ticket links, the relevant git history. Pull it from your own tools; Orako will
   not fetch or summarize context for you. Write it in Markdown.

5. **If a human is present, show a one-line recap and get their go-ahead before
   sending:** the **project** it posts to, the expert/pool targeted, and the exact
   question. In a fully headless run with no human to confirm, skip the
   confirmation and proceed — but still name, in your own logs, where you routed it.

6. **Asking is asynchronous — do NOT hard-block, and do NOT bail either.** Set
   `wait:true` on `Ask` to block briefly (~90s) for a live reply. On timeout you
   get a `conversationId`: work an **escalating ladder** — `GetConversation` at
   ~2 min, then ~5, then ~10 (space the checks; never tight-loop). Only after the
   ladder is empty, park it and continue other work; relay the answer when it
   lands. Presence is not a signal — someone can answer at any moment.

7. **Keep one thread with `FollowUp`** — to challenge an answer, ask for detail, or
   re-ask after an unclear reply. Never open a second `Ask` for a topic you already
   have an open conversation on (that spawns a duplicate and re-pings the expert).

8. **When resolved, `ResolveConversation` with a distilled Markdown resolution** —
   this is exactly what the next agent's `SearchHistory` surfaces. Start it with a
   one-line `## Heading` naming the topic (it becomes the title) and pass a short
   `tags` list. A vague resolution degrades the history for everyone.

## Rules that do not bend

- **`SearchHistory` before every `Ask`** — no exceptions. Branch on the hit status
  instead of opening a duplicate.
- **One topic, one thread.** Same person → `FollowUp`. Another person's take on an
  open thread → `AddParticipant`, not a new `Ask`.
- **Never fabricate a human's answer.** If the thread is still open, say so and
  offer to check back — do not guess on the human's behalf.
- **The LLM work is yours** (restating the question, tags, distilling the
  resolution). Orako stores and routes; it does not generate.
- A call outside your token's project or role returns a permission error — that is
  RBAC working as intended, not a bug to route around.

## How to reach Orako

Same operations, same token, two transports:

- **MCP** — if your framework speaks MCP, register Orako's MCP server; the tools
  (`SearchHistory`, `Ask`, `FollowUp`, `GetConversation`, `ListExperts`,
  `AddParticipant`, `ResolveConversation`, `ListProjects`) appear automatically.
- **Connect-JSON** — no MCP client? Call the exact same operations as plain HTTP
  `POST {BASE}/orako.v1.OrakoService/{Method}` with a JSON body. This is the path
  for a custom function-calling loop (e.g. Hermes), a CI job, or any non-MCP
  framework. Field names and copy-paste `curl` recipes are in
  [references/connect-json.md](../references/connect-json.md).

Both authenticate with an **Orako machine token** (`mcp_at_…`), minted by an org
admin in the dashboard (Settings → Machine tokens), passed as
`Authorization: Bearer mcp_at_…`. The token is project-scoped and revocable.
