---
name: orako-ask-a-human
description: >
  Use this WHENEVER you hit a decision only a human on the team can make and you
  would otherwise have to guess — product intent, the "why" behind a past
  decision, an ambiguous or under-specified requirement, tribal knowledge that is
  not in the code, docs, or tickets. Instead of guessing, route the question to
  the right human through Orako and reuse the answer forever. Also trigger this
  whenever the task mentions Orako, "ask a human", "ask the team", "ask an
  expert", "who owns this", "check with the PO/PM", or connecting an agent to
  human experts / conversation history. ESPECIALLY important in headless or
  autonomous runs (CI, background jobs, scheduled agents) where no human is at the
  keyboard to interrupt — there, asking through Orako is the only way to get a
  human decision instead of shipping a wrong guess.
---

# Orako — ask a human, reuse the answer forever

Orako connects you to your team's human experts and to every past answer. It is
**LLM-free**: it never generates answers — it *routes* your question to the right
person and *stores* what comes back, so a human is asked **once, not ten times**.

Reach for Orako at the exact moment you would otherwise **guess** on something
only a human can settle. Guessing on product intent or a past decision is how
agents confidently ship the wrong thing. A short async question is cheaper than a
wrong PR found in review — or in prod.

## When to use it (and when not)

**Use it for questions a human must answer:**
- Product intent / priorities ("which onboarding screen matters first?").
- The *why* behind a past decision, not written down anywhere.
- An ambiguous requirement you cannot resolve from the repo.
- Tribal knowledge: "does our WPML connector support multi-site?", "why is this
  flag off in prod?".

**Do NOT use it for things you can answer yourself.** Read the code, the docs, the
tickets, and your own tools first. Orako is the last resort *after* your own
sources come up empty — not a shortcut around reading the repo.

## The loop, in order

Follow this every time — it is what keeps the history clean and stops you from
re-pinging busy people.

1. **`search_history` FIRST — always, before asking a human.** Most questions were
   already asked once; searching is free and instant, asking a human costs their
   time. Search with the clearest restatement of the question; if the first search
   is empty, try one rephrasing before concluding it's a miss.

2. **Branch on each hit's `status`** — this is how history routes you instead of
   asking again:
   - `resolved` → **reuse it**. `get_conversation` on the hit's `conversation_id`
     for the full answer. (If it looks stale or disputed, ask fresh instead.)
   - `open` → the **same question is already in flight**. Do NOT open a duplicate —
     `add_participant` to join that thread and converge there.
   - `timed_out` / `dismissed` → it was asked but never answered. Re-ping the
     original `asker_member_id` via `ask`.
   - **no hit** → ask a fresh question (steps 3+).

3. **On a genuine miss, pick who to ask.** `list_experts` to see who is routable by
   `domains` (expertise tags). Target **one** expert by `member_id` when the owner
   is obvious; otherwise pass `domains` to reach every matching teammate — all
   replies land on one thread, the first answerer is recorded. Route on
   **expertise, never on presence** — anyone contacted can answer at any moment.
   (Multi-project org? `list_projects` and pick the project the question belongs
   to — never rely on silent auto-scoping.)
   - **Self-ask:** your own user is a valid target. In a long autonomous run where
     the person with the answer is the very human you work for and they are away
     from the terminal, `ask` THEM by their `member_id` — Orako reaches them on
     Discord/Slack, and you pick up their reply with `get_conversation`. Prefer
     that over stalling.

4. **Assemble a self-contained context packet before asking** — files, symbols,
   ticket links, the relevant git history. Pull it from your own tools; Orako will
   not fetch or summarize context for you. Write it in Markdown.

5. **If a human is present, show a one-line recap and get their go-ahead before
   sending:** the **project** it will post to, the expert/pool targeted, and the
   exact question. In a fully headless run with no human to confirm, skip the
   confirmation and proceed — but still name, in your own logs, where you routed it.

6. **Asking is asynchronous — do NOT hard-block, and do NOT bail either.** Set
   `wait=true` on `ask` to block briefly (~90s) for a live reply. If it times out
   you get a `conversation_id`: work an **escalating ladder** — `get_conversation`
   at ~2 min, then ~5, then ~10 (space the checks; never tight-loop). Only after
   the ladder is empty, park it and continue other work; relay the answer when it
   lands. Presence is not a signal — someone can answer at any moment.

7. **Keep one thread with `follow_up`** — to challenge an answer, ask for detail, or
   re-ask after an unclear reply. Never open a second `ask` for a topic you already
   have an open conversation on (that spawns a duplicate and re-pings the expert).

8. **When resolved, `resolve_conversation` with a distilled Markdown resolution** —
   this is exactly what the next agent's `search_history` surfaces. Start it with a
   one-line `## Heading` naming the topic (it becomes the title) and pass a short
   `tags` list. A vague resolution degrades the history for everyone.

## Rules that do not bend

- **`search_history` before every `ask`** — no exceptions. Branch on the hit status
  instead of opening a duplicate.
- **One topic, one thread.** Same person → `follow_up`. Another person's take on an
  open thread → `add_participant`, not a new `ask`.
- **Never fabricate a human's answer.** If the thread is still open, say so and
  offer to check back — do not guess on the human's behalf.
- **The LLM work is yours** (restating the question, tags, distilling the
  resolution). Orako stores and routes; it does not generate.
- A tool call outside your project or role fails with a permission error — that is
  RBAC working as intended, not a bug to route around.

## Two ways to reach Orako (same tools, same token)

**MCP (preferred when your framework speaks MCP).** Register Orako's MCP server;
the tools (`search_history`, `ask`, `follow_up`, `get_conversation`, `list_experts`,
`add_participant`, `resolve_conversation`, `list_projects`) appear automatically and
the server sends this same guidance on connect. Nothing else to install.

**Connect-JSON (for headless / non-MCP agents).** No MCP client? Call the exact same
operations as plain HTTP POST + JSON with your machine token. This is the path for a
custom loop, a CI job, or any framework without MCP. The request/response field
names and copy-paste `curl` recipes are in **[references/connect-json.md](references/connect-json.md)** —
read it when you are wiring a non-MCP agent.

Both paths authenticate with an **Orako machine token** (`mcp_at_…`), minted by an
org admin in the dashboard (Settings → Machine tokens) and passed as
`Authorization: Bearer mcp_at_…`. The token is scoped to specific projects and is
revocable.

## Same policy for non-Claude agents

This pack ships the identical guidance in framework-agnostic forms, so the same
ask-a-human loop applies whichever agent runs it:

- **`variants/AGENTS.md`** — paste into a project's `AGENTS.md` (Codex, Cursor,
  Aider, Jules, and any agent that reads the convention).
- **`variants/system-prompt.txt`** — a plain-text block to inject into any
  framework's system prompt (LangChain, a custom function-calling loop, a
  Hermes/tool-calling model).

Both point back to `references/connect-json.md` for the non-MCP HTTP transport.
