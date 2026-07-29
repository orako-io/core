import{m as G,b as F,r as s,j as e,T as n,I as h,P as K,h as J}from"./index-ByNrN3Mi.js";import{S as N}from"./Select-8ilmJFYi.js";const W=`# Orako — ask a human before you guess

> Drop-in \`AGENTS.md\` guidance for any agent that reads this convention (Codex,
> Cursor, Aider, Jules, Hermes-based loops, …). Paste this section into your
> project's \`AGENTS.md\`, or ship the file as-is. It carries the same policy as the
> Claude-Code \`SKILL.md\`; the transport detail (HTTP + JSON) is in
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

1. **\`SearchHistory\` FIRST — always, before asking a human.** Most questions were
   already asked once; searching is free and instant, asking a human costs their
   time. Search the clearest restatement of the question; if the first search is
   empty, try one rephrasing before concluding it is a miss.

2. **Branch on each hit's \`status\`** — this is how history routes you instead of
   asking again:
   - \`resolved\` → **reuse it.** \`GetConversation\` on the hit's \`conversationId\`
     for the full answer. (If it looks stale or disputed, ask fresh instead.)
   - \`open\` → the **same question is already in flight.** Do NOT open a duplicate —
     \`AddParticipant\` to join that thread and converge there.
   - \`timed_out\` / \`dismissed\` → asked but never answered. Re-ask the original
     \`askerMemberId\` via \`Ask\`.
   - **no hit** → ask a fresh question (steps 3+).

3. **On a genuine miss, pick who to ask.** \`ListExperts\` shows who is routable by
   \`domains\` (expertise tags). Target **one** expert by \`memberId\` when the owner
   is obvious; otherwise pass \`domains\` to reach every matching teammate — all
   replies land on one thread, the first answerer is recorded. Route on
   **expertise, never on presence** — anyone contacted can answer at any moment.
   Multi-project org? \`ListProjects\` and pick the project the question belongs to;
   never rely on silent auto-scoping.
   - **Self-ask:** your own user is a valid target. In a long autonomous run where
     the person with the answer is the very human you work for and they are away
     from the terminal, \`Ask\` THEM by their \`memberId\` — Orako reaches them on
     Discord/Slack and you pick up the reply with \`GetConversation\`. Prefer that
     over stalling.

4. **Assemble a self-contained context packet before asking** — files, symbols,
   ticket links, the relevant git history. Pull it from your own tools; Orako will
   not fetch or summarize context for you. Write it in Markdown.

5. **If a human is present, show a one-line recap and get their go-ahead before
   sending:** the **project** it posts to, the expert/pool targeted, and the exact
   question. In a fully headless run with no human to confirm, skip the
   confirmation and proceed — but still name, in your own logs, where you routed it.

6. **Asking is asynchronous — do NOT hard-block, and do NOT bail either.** Set
   \`wait:true\` on \`Ask\` to block briefly (~90s) for a live reply. On timeout you
   get a \`conversationId\`: work an **escalating ladder** — \`GetConversation\` at
   ~2 min, then ~5, then ~10 (space the checks; never tight-loop). Only after the
   ladder is empty, park it and continue other work; relay the answer when it
   lands. Presence is not a signal — someone can answer at any moment.

7. **Keep one thread with \`FollowUp\`** — to challenge an answer, ask for detail, or
   re-ask after an unclear reply. Never open a second \`Ask\` for a topic you already
   have an open conversation on (that spawns a duplicate and re-pings the expert).

8. **When resolved, \`ResolveConversation\` with a distilled Markdown resolution** —
   this is exactly what the next agent's \`SearchHistory\` surfaces. Start it with a
   one-line \`## Heading\` naming the topic (it becomes the title) and pass a short
   \`tags\` list. A vague resolution degrades the history for everyone.

## Rules that do not bend

- **\`SearchHistory\` before every \`Ask\`** — no exceptions. Branch on the hit status
  instead of opening a duplicate.
- **One topic, one thread.** Same person → \`FollowUp\`. Another person's take on an
  open thread → \`AddParticipant\`, not a new \`Ask\`.
- **Never fabricate a human's answer.** If the thread is still open, say so and
  offer to check back — do not guess on the human's behalf.
- **The LLM work is yours** (restating the question, tags, distilling the
  resolution). Orako stores and routes; it does not generate.
- A call outside your token's project or role returns a permission error — that is
  RBAC working as intended, not a bug to route around.

## How to reach Orako

Same operations, same token, two transports:

- **MCP** — if your framework speaks MCP, register Orako's MCP server; the tools
  (\`SearchHistory\`, \`Ask\`, \`FollowUp\`, \`GetConversation\`, \`ListExperts\`,
  \`AddParticipant\`, \`ResolveConversation\`, \`ListProjects\`) appear automatically.
- **Connect-JSON** — no MCP client? Call the exact same operations as plain HTTP
  \`POST {BASE}/orako.v1.OrakoService/{Method}\` with a JSON body. This is the path
  for a custom function-calling loop (e.g. Hermes), a CI job, or any non-MCP
  framework. Field names and copy-paste \`curl\` recipes are in
  [references/connect-json.md](../references/connect-json.md).

Both authenticate with an **Orako machine token** (\`mcp_at_…\`), minted by an org
admin in the dashboard (Settings → Machine tokens), passed as
\`Authorization: Bearer mcp_at_…\`. The token is project-scoped and revocable.
`,Y='# Orako over Connect-JSON (headless / non-MCP)\n\nFor an agent that does **not** speak MCP. Every Orako tool is also a plain HTTP\nendpoint: `POST {BASE}/orako.v1.OrakoService/{Method}` with a JSON body and your\nmachine token. No SDK required — `curl`, `fetch`, `requests`, anything.\n\n- **`{BASE}`** — your Orako API base, e.g. `https://api.orako.io` (self-host: your\n  own host).\n- **Auth** — `Authorization: Bearer mcp_at_…` (an Orako machine token; an org admin\n  mints it in the dashboard → Settings → Machine tokens; it is project-scoped and\n  revocable).\n- **Headers** — always `Content-Type: application/json`.\n- **JSON field names are camelCase** (`project_id` → `projectId`, `conversation_id`\n  → `conversationId`, `member_id` → `memberId`, `top_k` → `topK`).\n\nSet once:\n\n```bash\nexport ORAKO_BASE="https://api.orako.io"\nexport ORAKO_TOKEN="mcp_at_xxx"   # your machine token\norako() { curl -sS -X POST "$ORAKO_BASE/orako.v1.OrakoService/$1" \\\n  -H "Authorization: Bearer $ORAKO_TOKEN" -H "Content-Type: application/json" -d "$2"; }\n```\n\n## The four core operations\n\n### 1. SearchHistory — ALWAYS first\n\n```bash\norako SearchHistory \'{"query":"activer les updates auto WordPress","topK":5}\'\n```\nOptional: `"projectIds":["…"]` (default = every project the token can see),\n`"status":"resolved"`, `"tags":["wordpress"]`.\nResponse: `{ "hits": [ { "conversationId","title","summary","status",\n"askerMemberId","tags","entities","projectId",… } ], "knownTags":[…],\n"knownEntities":[…] }`. **Branch on each hit\'s `status`** (resolved→reuse,\nopen→AddParticipant, timed_out/dismissed→re-ask, none→Ask).\n\n### 2. Ask — only after a miss\n\nDirect to one expert:\n```bash\norako Ask \'{\n  "memberId":"<expert-member-id>",\n  "question":"PushRank gère-t-il le multi-site WPML ?",\n  "context":"## Contexte\\nUtilisateur sur WordPress multisite…",\n  "title":"Support WPML multi-site",\n  "wait":true\n}\'\n```\nOr dispatch to a pool by expertise (omit `memberId`, pass `domains`):\n```bash\norako Ask \'{"domains":["integrations"],"question":"…","wait":true}\'\n```\nExactly one of `memberId` / `domains`. `projectId` is optional when the token\nscopes one project; **required** when it scopes several. `wait:true` blocks ~90s\nfor a live reply.\nResponse: `{ "conversationId","answered","inlineAnswer","poolSize",\n"projectName","recipientNames":[…] }`. If `answered:true`, use `inlineAnswer`.\nOtherwise keep the `conversationId` and poll.\n\n### 3. GetConversation — poll for the answer / read the thread\n\n```bash\norako GetConversation \'{"conversationId":"<id>"}\'\n```\nResponse: `{ "status":"open|answered|resolved|timed_out",\n"messages":[{"authorMemberId","body","source","at",…}], "participants":[…] }`.\nWork the ladder: after an `Ask` timeout, poll at **~2 min, ~5 min, ~10 min** —\nspace the checks, never tight-loop. `status` leaves `open`/`answered` when a human\nhas replied; read the latest `messages[].body` where `source:"human"`.\n\n### 4. FollowUp — keep the same thread\n\n```bash\norako FollowUp \'{"conversationId":"<id>","body":"Merci — et pour le mode sous-domaine ?"}\'\n```\nUse this (not a new `Ask`) to challenge, clarify, or re-ask on a thread you already\nopened — a fresh `Ask` would spawn a duplicate and re-ping the expert.\n\n## Minimal headless flow (pseudocode)\n\n```\nhits = SearchHistory(query)\nif a hit is resolved:            reuse GetConversation(hit.conversationId)   # done, no human bothered\nelif a hit is open:              AddParticipant(hit.conversationId, me); FollowUp(...)\nelse:\n    r = Ask(question, context, memberId|domains, wait=true)\n    if r.answered:  use r.inlineAnswer\n    else:\n        for delay in [120s, 300s, 600s]:\n            sleep(delay)\n            c = GetConversation(r.conversationId)\n            if c has a human reply: use it; break\n        else: park — continue other work, relay the answer when it lands\n# when settled:\nResolveConversation(conversationId, "## Heading\\n<distilled markdown answer>", tags=[…])\n```\n\n## Also available (same pattern)\n\n`ListExperts` (who is routable, by `domains`), `ListProjects`, `ListConversations`\n(`{"status":"open"}` to find threads waiting on you), `AddParticipant`\n(`{"conversationId","memberId"}`), `ResolveConversation`\n(`{"conversationId","resolution","tags":[…]}`). A call outside your token\'s\nproject/role returns a permission error — that is RBAC, not a bug.\n\n> The policy (when to ask, the search-first loop, one-topic-one-thread) is in\n> `SKILL.md`. This file is only the transport detail.\n',V=`---
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

1. **\`search_history\` FIRST — always, before asking a human.** Most questions were
   already asked once; searching is free and instant, asking a human costs their
   time. Search with the clearest restatement of the question; if the first search
   is empty, try one rephrasing before concluding it's a miss.

2. **Branch on each hit's \`status\`** — this is how history routes you instead of
   asking again:
   - \`resolved\` → **reuse it**. \`get_conversation\` on the hit's \`conversation_id\`
     for the full answer. (If it looks stale or disputed, ask fresh instead.)
   - \`open\` → the **same question is already in flight**. Do NOT open a duplicate —
     \`add_participant\` to join that thread and converge there.
   - \`timed_out\` / \`dismissed\` → it was asked but never answered. Re-ping the
     original \`asker_member_id\` via \`ask\`.
   - **no hit** → ask a fresh question (steps 3+).

3. **On a genuine miss, pick who to ask.** \`list_experts\` to see who is routable by
   \`domains\` (expertise tags). Target **one** expert by \`member_id\` when the owner
   is obvious; otherwise pass \`domains\` to reach every matching teammate — all
   replies land on one thread, the first answerer is recorded. Route on
   **expertise, never on presence** — anyone contacted can answer at any moment.
   (Multi-project org? \`list_projects\` and pick the project the question belongs
   to — never rely on silent auto-scoping.)
   - **Self-ask:** your own user is a valid target. In a long autonomous run where
     the person with the answer is the very human you work for and they are away
     from the terminal, \`ask\` THEM by their \`member_id\` — Orako reaches them on
     Discord/Slack, and you pick up their reply with \`get_conversation\`. Prefer
     that over stalling.

4. **Assemble a self-contained context packet before asking** — files, symbols,
   ticket links, the relevant git history. Pull it from your own tools; Orako will
   not fetch or summarize context for you. Write it in Markdown.

5. **If a human is present, show a one-line recap and get their go-ahead before
   sending:** the **project** it will post to, the expert/pool targeted, and the
   exact question. In a fully headless run with no human to confirm, skip the
   confirmation and proceed — but still name, in your own logs, where you routed it.

6. **Asking is asynchronous — do NOT hard-block, and do NOT bail either.** Set
   \`wait=true\` on \`ask\` to block briefly (~90s) for a live reply. If it times out
   you get a \`conversation_id\`: work an **escalating ladder** — \`get_conversation\`
   at ~2 min, then ~5, then ~10 (space the checks; never tight-loop). Only after
   the ladder is empty, park it and continue other work; relay the answer when it
   lands. Presence is not a signal — someone can answer at any moment.

7. **Keep one thread with \`follow_up\`** — to challenge an answer, ask for detail, or
   re-ask after an unclear reply. Never open a second \`ask\` for a topic you already
   have an open conversation on (that spawns a duplicate and re-pings the expert).

8. **When resolved, \`resolve_conversation\` with a distilled Markdown resolution** —
   this is exactly what the next agent's \`search_history\` surfaces. Start it with a
   one-line \`## Heading\` naming the topic (it becomes the title) and pass a short
   \`tags\` list. A vague resolution degrades the history for everyone.

## Rules that do not bend

- **\`search_history\` before every \`ask\`** — no exceptions. Branch on the hit status
  instead of opening a duplicate.
- **One topic, one thread.** Same person → \`follow_up\`. Another person's take on an
  open thread → \`add_participant\`, not a new \`ask\`.
- **Never fabricate a human's answer.** If the thread is still open, say so and
  offer to check back — do not guess on the human's behalf.
- **The LLM work is yours** (restating the question, tags, distilling the
  resolution). Orako stores and routes; it does not generate.
- A tool call outside your project or role fails with a permission error — that is
  RBAC working as intended, not a bug to route around.

## Two ways to reach Orako (same tools, same token)

**MCP (preferred when your framework speaks MCP).** Register Orako's MCP server;
the tools (\`search_history\`, \`ask\`, \`follow_up\`, \`get_conversation\`, \`list_experts\`,
\`add_participant\`, \`resolve_conversation\`, \`list_projects\`) appear automatically and
the server sends this same guidance on connect. Nothing else to install.

**Connect-JSON (for headless / non-MCP agents).** No MCP client? Call the exact same
operations as plain HTTP POST + JSON with your machine token. This is the path for a
custom loop, a CI job, or any framework without MCP. The request/response field
names and copy-paste \`curl\` recipes are in **[references/connect-json.md](references/connect-json.md)** —
read it when you are wiring a non-MCP agent.

Both paths authenticate with an **Orako machine token** (\`mcp_at_…\`), minted by an
org admin in the dashboard (Settings → Machine tokens) and passed as
\`Authorization: Bearer mcp_at_…\`. The token is scoped to specific projects and is
revocable.

## Same policy for non-Claude agents

This pack ships the identical guidance in framework-agnostic forms, so the same
ask-a-human loop applies whichever agent runs it:

- **\`variants/AGENTS.md\`** — paste into a project's \`AGENTS.md\` (Codex, Cursor,
  Aider, Jules, and any agent that reads the convention).
- **\`variants/system-prompt.txt\`** — a plain-text block to inject into any
  framework's system prompt (LangChain, a custom function-calling loop, a
  Hermes/tool-calling model).

Both point back to \`references/connect-json.md\` for the non-MCP HTTP transport.
`,B=`ORAKO — ASK A HUMAN BEFORE YOU GUESS

[ Paste this block into your agent's system prompt. It is framework-agnostic:
  LangChain, LlamaIndex, a custom function-calling loop, a Hermes/tool-calling
  model — anything. Keep the tool/operation names; wire them to Orako via MCP or
  via HTTP POST {BASE}/orako.v1.OrakoService/{Method} (field names + curl recipes
  are in references/connect-json.md). Auth: Authorization: Bearer mcp_at_… ]

You have access to Orako: a bridge to your team's human experts and to every past
answer they have given. Orako is LLM-FREE — it never writes answers. It ROUTES a
question to the right person and STORES what comes back, so a human is asked once,
not ten times.

Use Orako at the moment you would otherwise GUESS on something only a human can
settle: product intent, the reason behind a past decision, an under-specified
requirement, or tribal knowledge that is not in the code, docs, or tickets. This
is critical in headless/autonomous runs where no human can interrupt you — asking
is the only way to avoid shipping a confident wrong guess. Do NOT use Orako for
anything you can answer yourself: read the code, docs, tickets, and your own tools
first. It is a last resort, not a shortcut around reading the repo.

THE LOOP (follow in order):
1. SearchHistory FIRST, always, before asking a human. Search the clearest
   restatement of the question; if empty, rephrase once before deciding it is a
   miss.
2. Branch on each hit's status:
   - resolved  -> reuse it: GetConversation(conversationId) for the full answer.
   - open      -> the same question is already in flight: AddParticipant to join
                  that thread; do NOT open a duplicate.
   - timed_out / dismissed -> asked but never answered: re-ask askerMemberId.
   - no hit    -> ask fresh (steps 3+).
3. On a genuine miss, ListExperts to see who is routable by domains (expertise).
   Target one expert by memberId when the owner is obvious; otherwise pass domains
   to reach every matching teammate (first answerer is recorded). Route on
   expertise, never on presence. Your own user is a valid target when they are the
   one who knows and is away from the terminal.
4. Assemble a self-contained context packet first (files, symbols, ticket links,
   git history) in Markdown. Orako will not fetch or summarize context for you.
5. If a human is present, show a one-line recap (project, target, exact question)
   and get their go-ahead before sending. Headless with no one to confirm: proceed,
   but log where you routed it.
6. Asking is asynchronous. Set wait=true to block ~90s for a live reply. On timeout
   you get a conversationId: poll GetConversation on an escalating ladder — ~2 min,
   then ~5, then ~10; never tight-loop. If still empty, park it, keep working, and
   relay the answer when it lands. Someone can answer at any moment.
7. Keep one thread: FollowUp to challenge, clarify, or re-ask. Never open a second
   Ask for a topic you already have an open conversation on.
8. When settled, ResolveConversation with a distilled Markdown resolution starting
   with a one-line "## Heading" (it becomes the title) plus a short tags list —
   this is exactly what the next agent's SearchHistory will surface.

RULES THAT DO NOT BEND:
- SearchHistory before every Ask — no exceptions.
- One topic, one thread: same person -> FollowUp; another person on an open thread
  -> AddParticipant; never a duplicate Ask.
- Never fabricate a human's answer. If the thread is still open, say so.
- The reasoning work is yours (restating, tagging, distilling). Orako only routes
  and stores.
- A call outside your token's project/role returns a permission error — that is
  RBAC working, not a bug to route around.
`,X="/assets/orako-ask-a-human-BMEfUMeG.skill",z={"claude-code":{label:"Claude Code",register:t=>`claude mcp add --transport http orako ${t}`,authorize:"Run /mcp inside Claude Code to approve.",note:"Usage: tools are called from natural language, and the Orako prompts also appear as slash commands — /mcp__orako__orako_answer (reply to the thread you were pinged on) and /mcp__orako__orako_sync (live back-and-forth). Reconnect /mcp after a server update to refresh them."},codex:{label:"Codex",register:t=>`codex mcp add orako --url ${t}`,authorize:"Run codex mcp login orako to approve.",note:"Usage: Codex has no per-tool slash commands — drive Orako in natural language, e.g. “list my open orako conversations, then reply to the latest via follow_up: <message>”. Type /mcp in the Codex TUI to check the connection; if a thread doesn’t show up, ask it to check your other projects (list_projects)."},gemini:{label:"Gemini CLI",register:t=>`gemini mcp add -t http orako ${t}`,authorize:"Run /mcp auth orako to approve."},copilot:{label:"GitHub Copilot (CLI)",register:t=>`copilot mcp add --transport http orako ${t}`,authorize:"Run copilot -i /mcp to approve."},cursor:{label:"Cursor",json:".cursor/mcp.json",path:".cursor/mcp.json",register:t=>`{
  "mcpServers": {
    "orako": { "type": "http", "url": "${t}" }
  }
}`,authorize:"Approve in the browser — Cursor prompts you automatically."},vscode:{label:"VS Code",json:".vscode/mcp.json",path:".vscode/mcp.json",register:t=>`{
  "servers": {
    "orako": { "type": "http", "url": "${t}" }
  }
}`,authorize:"Approve in the browser — VS Code prompts you automatically."},windsurf:{label:"Windsurf",json:"~/.codeium/windsurf/mcp_config.json",path:"~/.codeium/windsurf/mcp_config.json",register:t=>`{
  "mcpServers": {
    "orako": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "${t}"]
    }
  }
}`,authorize:"Approve in the browser — auto-prompted on first use.",note:"Uses the generic mcp-remote npm proxy (not an Orako binary). Newer Windsurf builds have a native serverUrl field — prefer it if your version offers it."}},Z=["claude-code","codex","gemini","copilot","cursor","vscode","windsurf"],M={"claude-desktop":{label:"Claude Desktop",steps:["Open Claude Desktop → profile picture → Settings → Connectors.",'Click "Add" next to Connectors.','Paste the MCP URL above into the URL field and click "Add".','Click "Connect" and approve access in the browser tab that opens.'],plan:"Available on Free, Pro, Max, Team and Enterprise (Free is limited to one custom connector). Claude reaches your server over the public internet, so it needs a publicly reachable deployment."},"claude-ai":{label:"Claude.ai (web)",steps:["Open claude.ai → Settings → Customize → Connectors.",'Click the "+" next to Connectors, then "Add custom connector".','Paste the MCP URL above into the URL field and click "Add".','Click "Connect" and approve access in the browser tab that opens.'],plan:"Same Connectors UI as Claude Desktop, on Free, Pro, Max, Team and Enterprise (Free is limited to one custom connector)."},chatgpt:{label:"ChatGPT",steps:["Open ChatGPT → Plugins → Developer mode.","Enable Developer Mode (first time only).",'Open Plugins → Browse Plugins, then click "+".',"Name the connector, paste the MCP URL above, then connect and approve access."],plan:"Requires a paid plan (Plus, Pro, Business, Enterprise or Edu). OpenAI flags Developer-mode connectors as able to take real actions on your behalf, so only connect servers you trust."}},Q=["claude-desktop","claude-ai","chatgpt"],ee={"claude-code":"skill",codex:"agents",gemini:"agents",copilot:"agents",cursor:"agents",vscode:"agents",windsurf:"agents"},E={background:n.surface,border:`1px solid ${n.border}`,borderRadius:n.rXl,overflow:"hidden"},O={background:"#1B1F2A",borderRadius:11,padding:"14px 16px"},A={fontFamily:n.mono,fontSize:13,color:"#E4E7EC"},w={fontSize:11.5,fontWeight:600,letterSpacing:".03em",color:n.subtle},q={border:`1px solid ${n.accentBorder}`,background:n.surface,color:n.accentHover,borderRadius:9,padding:"9px 12px",display:"inline-flex",alignItems:"center",gap:7,fontFamily:"inherit",fontSize:12.5,fontWeight:650,cursor:"pointer",textDecoration:"none"};function _({onCopy:t}){return e.jsx("button",{type:"button",onClick:t,title:"Copy",style:{border:"none",background:"transparent",padding:0,cursor:"pointer",color:n.subtle,display:"inline-flex",flex:"none"},children:e.jsx(h,{name:"copy",size:15})})}function C({children:t,icon:a,onClick:r}){return e.jsxs("button",{type:"button",onClick:r,style:q,children:[e.jsx(h,{name:a,size:14,strokeWidth:1.9}),t]})}function D({children:t}){return e.jsx("pre",{style:{...O,...A,margin:0,maxHeight:150,overflow:"hidden",whiteSpace:"pre-wrap",lineHeight:1.55,maskImage:"linear-gradient(to bottom, black 65%, transparent 100%)",WebkitMaskImage:"linear-gradient(to bottom, black 65%, transparent 100%)"},children:t})}function S({n:t,title:a,mt:r=26}){return e.jsxs("div",{style:{marginTop:r,display:"flex",alignItems:"center",gap:10},children:[e.jsx("span",{style:{width:24,height:24,borderRadius:"50%",background:n.accentSoft,color:n.accent,display:"flex",alignItems:"center",justifyContent:"center",fontSize:13,fontWeight:700,flex:"none"},children:t}),e.jsx("span",{style:{fontSize:15.5,fontWeight:600,color:n.body},children:a})]})}function U({children:t}){return e.jsxs("div",{style:{display:"flex",gap:10,alignItems:"flex-start",background:n.warnBg,border:`1px solid ${n.warnBorder}`,borderRadius:11,padding:"12px 14px"},children:[e.jsx(h,{name:"alertTriangle",size:16,color:n.warn,strokeWidth:1.9,style:{flex:"none",marginTop:1}}),e.jsx("p",{style:{fontSize:12.5,lineHeight:1.55,color:n.warn,margin:0},children:t})]})}function $({active:t,onSelect:a,title:r,desc:v,badge:u}){return e.jsxs("button",{onClick:a,style:{textAlign:"left",cursor:"pointer",padding:"16px 17px",borderRadius:14,background:n.surface,display:"flex",flexDirection:"column",gap:6,transition:"all .15s",border:t?`2px solid ${n.accent}`:`1px solid ${n.borderStrong}`,boxShadow:t?n.shadowPop:"none",fontFamily:"inherit"},children:[e.jsxs("div",{style:{display:"flex",alignItems:"center",gap:9},children:[e.jsx("span",{style:{width:18,height:18,borderRadius:"50%",flex:"none",border:t?`5px solid ${n.accent}`:"2px solid #C7CBD4",background:t?n.accent:"transparent"}}),e.jsx("span",{style:{fontSize:14.5,fontWeight:700,color:n.text},children:r}),u&&e.jsx("span",{style:{marginLeft:"auto",fontSize:10.5,fontWeight:700,letterSpacing:".04em",color:n.accentHover,background:n.accentSoft,border:`1px solid ${n.accentBorder}`,padding:"3px 7px",borderRadius:6},children:u})]}),e.jsx("p",{style:{fontSize:13,lineHeight:1.5,color:n.muted,paddingLeft:27,margin:0},children:v})]})}function ae(){return e.jsxs(K,{width:760,children:[e.jsx("h2",{style:{fontSize:27,fontWeight:700,letterSpacing:"-.025em",color:"#12141B"},children:"Connect your agent to Orako"}),e.jsx("p",{style:{fontSize:15,lineHeight:1.6,color:n.muted,marginTop:9,maxWidth:640},children:"Point your coding agent — or Claude Desktop, ChatGPT — at Orako and authorize once. It gets your team's knowledge and teammates on tap. You authorize inside the agent; there's no token to copy."}),e.jsx(ne,{})]})}function ne(){const t=G(),{projects:a,selectedProjectId:r}=F(),[v,u]=s.useState("terminal"),[y,T]=s.useState("claude-code"),[b,m]=s.useState("claude-desktop"),[d,p]=s.useState(!1),[j,i]=s.useState(!1),c=`${window.location.origin}/mcp`,k=v==="terminal",l=z[y]??z["claude-code"],I=M[b]??M["claude-desktop"],x=k?l.label:I.label,H=k?ee[y]??"agents":"mcp",P=a.find(o=>o.id===r),R=P?`When using Orako, default to project "${P.name}" (project_id: "${P.id}") unless I explicitly name another project.`:"";async function g(o){try{await navigator.clipboard.writeText(o),t.success("Copied")}catch{t.error("Copy failed")}}return e.jsxs(e.Fragment,{children:[e.jsx(S,{n:1,title:"How do you want to connect?",mt:34}),e.jsxs("div",{style:{display:"grid",gridTemplateColumns:"1fr 1fr",gap:14,marginTop:14},children:[e.jsx($,{active:k,onSelect:()=>u("terminal"),title:"In my terminal / IDE",badge:"RECOMMENDED",desc:"For developers. The CLI registers the remote server for you."}),e.jsx($,{active:!k,onSelect:()=>u("noterminal"),title:"No terminal",desc:"Claude Desktop, Claude.ai, ChatGPT — connect from the app's settings with a URL."})]}),e.jsx(S,{n:2,title:"Which client?"}),e.jsx("div",{style:{marginTop:13,maxWidth:340},children:k?e.jsx(N,{value:y,onChange:o=>T(o.target.value),style:{height:46,fontWeight:600},children:Z.map(o=>e.jsx("option",{value:o,children:z[o].label},o))}):e.jsx(N,{value:b,onChange:o=>m(o.target.value),style:{height:46,fontWeight:600},children:Q.map(o=>e.jsx("option",{value:o,children:M[o].label},o))})}),e.jsx(S,{n:3,title:`Set up ${x}`}),k?e.jsxs("div",{style:{...E,marginTop:14},children:[e.jsxs("div",{style:{padding:"16px 20px",background:n.surfaceAlt,borderBottom:`1px solid ${n.borderSubtle}`,display:"flex",gap:11,alignItems:"flex-start"},children:[e.jsx(h,{name:"zap",size:18,color:n.accent,strokeWidth:1.9,style:{flex:"none",marginTop:1}}),e.jsxs("p",{style:{fontSize:13.5,lineHeight:1.55,color:"#3A414D",margin:0},children:[e.jsx("strong",{style:{color:n.text},children:"One registration."})," Point ",x," at the remote MCP endpoint, then authorize once inside the agent — nothing to copy-paste, no CLI to install. The agent gets Orako's tools immediately; the next step makes the recommended workflow explicit."]})]}),e.jsxs("div",{style:{padding:"22px 22px 24px",display:"flex",flexDirection:"column",gap:18},children:[e.jsxs("div",{children:[e.jsxs("div",{style:{...w,marginBottom:7},children:["REGISTER",l.path&&e.jsxs(e.Fragment,{children:[" · ",e.jsx("span",{style:{fontFamily:n.mono,color:n.muted,letterSpacing:0},children:l.path})]})]}),e.jsxs("div",{style:{...O,display:"flex",alignItems:"flex-start",gap:12},children:[e.jsxs("code",{style:{...A,fontSize:12.5,flex:1,overflowX:"auto",whiteSpace:l.json?"pre":"nowrap",lineHeight:1.6},children:[!l.json&&e.jsx("span",{style:{color:n.subtle},children:"$ "}),l.register(c)]}),e.jsx(_,{onCopy:()=>void g(l.register(c))})]}),l.note&&e.jsx("p",{style:{fontSize:12,lineHeight:1.5,color:n.faint,margin:"8px 0 0"},children:l.note}),e.jsx("div",{style:{...w,margin:"16px 0 7px"},children:"AUTHORIZE"}),e.jsxs("div",{style:{display:"flex",alignItems:"center",gap:10,background:n.bg,border:`1px solid ${n.borderSubtle}`,borderRadius:10,padding:"11px 14px"},children:[e.jsx(h,{name:"lock",size:16,color:n.accent,strokeWidth:1.9,style:{flex:"none"}}),e.jsx("span",{style:{fontSize:13.5,color:"#3A414D"},children:l.authorize})]})]}),e.jsx(U,{children:"MCP always exposes the tools. Some clients also relay Orako's server instructions to the model; the next step reinforces the workflow where that behavior is not guaranteed."})]})]}):e.jsxs("div",{style:{...E,marginTop:14},children:[e.jsxs("div",{style:{padding:"16px 20px",background:n.surfaceAlt,borderBottom:`1px solid ${n.borderSubtle}`,display:"flex",gap:11,alignItems:"flex-start"},children:[e.jsx(h,{name:"shield",size:18,color:n.accent,strokeWidth:1.9,style:{flex:"none",marginTop:1}}),e.jsxs("p",{style:{fontSize:13.5,lineHeight:1.55,color:"#3A414D",margin:0},children:[e.jsx("strong",{style:{color:n.text},children:"No install, no config file."})," These apps add a remote MCP server from their own settings: paste one URL, connect, approve in your browser. You get Orako's tools and their built-in descriptions."]})]}),e.jsxs("div",{style:{padding:22,display:"flex",flexDirection:"column",gap:20},children:[e.jsxs("div",{children:[e.jsx("div",{style:{...w,marginBottom:7},children:"THE ONLY URL — SAME EVERYWHERE"}),e.jsxs("div",{style:{...O,display:"flex",alignItems:"center",gap:12},children:[e.jsx("code",{style:{...A,fontSize:13.5,flex:1,overflowX:"auto",whiteSpace:"nowrap"},children:c}),e.jsx(_,{onCopy:()=>void g(c)})]})]}),e.jsxs("div",{children:[e.jsxs("div",{style:{fontSize:14.5,fontWeight:600,color:n.body,marginBottom:12},children:["Steps in ",x]}),e.jsx("div",{style:{display:"flex",flexDirection:"column"},children:I.steps.map((o,L)=>e.jsxs("div",{style:{display:"flex",gap:13,alignItems:"flex-start",paddingBottom:14},children:[e.jsx("span",{style:{width:24,height:24,borderRadius:7,background:n.accentSoft,color:n.accent,display:"flex",alignItems:"center",justifyContent:"center",fontSize:12.5,fontWeight:700,flex:"none"},children:L+1}),e.jsx("span",{style:{fontSize:13.5,lineHeight:1.5,color:"#3A414D",paddingTop:2},children:o})]},L))})]}),e.jsxs(U,{children:[I.plan," The exact menu wording and plan requirement change often — if your plan can't add a custom connector, you'll see it there rather than hunting."]})]})]}),e.jsx(te,{resource:c,onStatusChange:i}),!j&&e.jsx("div",{style:{marginTop:14,padding:"12px 15px",borderRadius:12,border:`1px dashed ${n.borderStrong}`,background:n.surfaceAlt,color:n.muted,fontSize:13,lineHeight:1.5},children:"The optional agent playbook appears after Orako confirms at least one authorization."}),e.jsxs("div",{style:{display:j?"contents":"none"},"aria-hidden":!j,children:[e.jsx(S,{n:4,title:"Teach your agent the Orako loop"}),e.jsxs("div",{style:{...E,marginTop:14},children:[e.jsxs("div",{style:{display:"grid",gridTemplateColumns:"1fr 1fr",background:n.surfaceAlt,borderBottom:`1px solid ${n.borderSubtle}`},children:[e.jsxs("div",{style:{padding:"15px 18px",borderRight:`1px solid ${n.borderSubtle}`},children:[e.jsx("div",{style:{...w,color:n.accent},children:"TOOLS"}),e.jsx("div",{style:{fontSize:13.5,fontWeight:650,color:n.body,marginTop:4},children:"Automatic over MCP"}),e.jsx("p",{style:{fontSize:12,lineHeight:1.5,color:n.muted,margin:"4px 0 0"},children:"Search, ask, follow up, and resolve are available after authorization."})]}),e.jsxs("div",{style:{padding:"15px 18px"},children:[e.jsx("div",{style:{...w,color:n.accent},children:"PLAYBOOK"}),e.jsx("div",{style:{fontSize:13.5,fontWeight:650,color:n.body,marginTop:4},children:"Client-dependent"}),e.jsx("p",{style:{fontSize:12,lineHeight:1.5,color:n.muted,margin:"4px 0 0"},children:"This locks in search-first, one-thread, and distilled resolution."})]})]}),e.jsxs("div",{style:{padding:22},children:[e.jsx("div",{role:"group","aria-label":"Agent guidance format",style:{display:"inline-flex",padding:3,gap:3,borderRadius:10,background:n.bg,border:`1px solid ${n.borderSubtle}`,marginBottom:18},children:[{active:!d,label:`For ${x}`,onClick:()=>p(!1)},{active:d,label:"Custom / headless",onClick:()=>p(!0)}].map(o=>e.jsx("button",{type:"button",onClick:o.onClick,"aria-pressed":o.active,style:{border:"none",borderRadius:7,padding:"7px 11px",background:o.active?n.surface:"transparent",boxShadow:o.active?n.shadowCard:"none",color:o.active?n.text:n.muted,fontFamily:"inherit",fontSize:12.5,fontWeight:o.active?650:500,cursor:"pointer"},children:o.label},o.label))}),d?e.jsxs("div",{style:{display:"flex",flexDirection:"column",gap:14},children:[e.jsxs("div",{children:[e.jsx("div",{style:{fontSize:15,fontWeight:700,color:n.text},children:"Paste into your system prompt"}),e.jsxs("p",{style:{fontSize:13,lineHeight:1.55,color:n.muted,margin:"5px 0 0",maxWidth:620},children:["For Hermes, CI, or a custom agent loop. If it does not speak MCP, also copy the Connect-JSON recipes and mint a scoped token in ",e.jsx("a",{href:"/machine-tokens",style:{color:n.accentHover},children:"Settings → Machine tokens"}),"."]})]}),e.jsx(D,{children:B}),e.jsxs("div",{style:{display:"flex",flexWrap:"wrap",gap:9},children:[e.jsx(C,{icon:"copy",onClick:()=>void g(B),children:"Copy system prompt"}),e.jsx(C,{icon:"copy",onClick:()=>void g(Y),children:"Copy Connect-JSON recipes"})]})]}):H==="skill"?e.jsxs("div",{style:{display:"flex",flexDirection:"column",gap:14},children:[e.jsxs("div",{children:[e.jsxs("div",{style:{display:"flex",alignItems:"center",gap:8},children:[e.jsx("span",{style:{fontSize:15,fontWeight:700,color:n.text},children:"Already delivered by Claude Code"}),e.jsx("span",{style:{...w,color:n.accent,background:n.accentSoft,borderRadius:5,padding:"3px 6px"},children:"OPTIONAL"})]}),e.jsx("p",{style:{fontSize:13,lineHeight:1.55,color:n.muted,margin:"5px 0 0",maxWidth:620},children:"Claude Code relays Orako's MCP instructions. Install the skill only if you want stronger triggering when a task reaches a human-only decision."})]}),e.jsxs("div",{style:{display:"flex",flexWrap:"wrap",gap:9},children:[e.jsx(C,{icon:"copy",onClick:()=>void g(V),children:"Copy SKILL.md"}),e.jsxs("a",{href:X,download:"orako-ask-a-human.skill",style:q,children:[e.jsx(h,{name:"download",size:14,strokeWidth:1.9}),"Download .skill"]})]})]}):H==="agents"?e.jsxs("div",{style:{display:"flex",flexDirection:"column",gap:14},children:[e.jsxs("div",{children:[e.jsx("div",{style:{fontSize:15,fontWeight:700,color:n.text},children:"Add this to your project's AGENTS.md"}),e.jsxs("p",{style:{fontSize:13,lineHeight:1.55,color:n.muted,margin:"5px 0 0",maxWidth:620},children:["MCP provides the tools. Project guidance guarantees the search-first workflow even when ",x," ","does not relay the server's instruction block."]})]}),e.jsx(D,{children:W}),e.jsx("div",{children:e.jsx(C,{icon:"copy",onClick:()=>void g(W),children:"Copy AGENTS.md guidance"})})]}):e.jsxs("div",{children:[e.jsx("div",{style:{fontSize:15,fontWeight:700,color:n.text},children:"No extra file to install"}),e.jsxs("p",{style:{fontSize:13,lineHeight:1.55,color:n.muted,margin:"5px 0 0",maxWidth:620},children:[x," receives the Orako tools and their descriptions through MCP. Use the custom/headless tab only if your client gives you a place to add persistent instructions."]})]}),R&&e.jsxs("div",{style:{marginTop:20,paddingTop:18,borderTop:`1px solid ${n.borderSubtle}`},children:[e.jsx("div",{style:{fontSize:14,fontWeight:700,color:n.text},children:"Optional: pin this project"}),e.jsx("p",{style:{fontSize:12.5,lineHeight:1.55,color:n.muted,margin:"5px 0 10px"},children:"The first project selected during authorization is already the default. For a multi-project agent, add this line to CLAUDE.md, AGENTS.md, or its persistent instructions."}),e.jsxs("div",{style:{...O,display:"flex",alignItems:"flex-start",gap:12},children:[e.jsx("code",{style:{...A,fontSize:12.5,flex:1,whiteSpace:"pre-wrap",lineHeight:1.55},children:R}),e.jsx(_,{onCopy:()=>void g(R)})]})]})]})]}),e.jsx(S,{n:5,title:"Did it work?"}),e.jsxs("div",{style:{marginTop:13,background:n.accentSofter,border:`1px solid ${n.accentBorder}`,borderRadius:14,padding:"16px 18px",display:"flex",gap:12,alignItems:"flex-start"},children:[e.jsx(h,{name:"message",size:19,color:n.accent,strokeWidth:1.9,style:{flex:"none",marginTop:1}}),e.jsxs("p",{style:{fontSize:13.5,lineHeight:1.6,color:"#3A414D",margin:0},children:["There's nothing to test here — Orako runs inside your agent, not the dashboard. Open your agent and ask it to use an Orako tool: ",e.jsx("em",{style:{color:n.accentHover},children:'"search Orako for the refund policy"'})," or"," ",e.jsx("em",{style:{color:n.accentHover},children:'"list the teammates on this project."'})," If it answers, you're connected. If it says the server needs authentication, run its authorize step (e.g."," ",e.jsx("code",{style:{fontFamily:n.mono,fontSize:12,background:n.accentSoft,color:n.accentHover,padding:"1px 6px",borderRadius:5},children:"/mcp"})," ","in Claude Code)."]})]})]})]})}function te({resource:t,onStatusChange:a}){const[r,v]=s.useState([]),[u,y]=s.useState(!0),[T,b]=s.useState(!1),m=s.useCallback(async()=>{b(!1);try{const f=await J.listMcpConnections();v((f.connections??[]).filter(c=>c.resource===t))}catch{b(!0)}finally{y(!1)}},[t]);s.useEffect(()=>(m(),window.addEventListener("focus",m),()=>window.removeEventListener("focus",m)),[m]),s.useEffect(()=>{a==null||a(r.length>0)},[r.length,a]);const d=s.useMemo(()=>[...r].sort((f,c)=>(c.connectedAt??"").localeCompare(f.connectedAt??"")),[r]),p=s.useMemo(()=>[...new Set(d.map(f=>f.clientName||"MCP client"))],[d]),j=p.length>1?`${p[0]} and ${p.length-1} more`:p[0],i=d.length>0;return e.jsxs("div",{style:{marginTop:14,border:`1px solid ${i?n.successBorder:n.border}`,background:i?n.successBg:n.surface,borderRadius:12,padding:"13px 15px",display:"flex",alignItems:"center",gap:11},children:[e.jsx(h,{name:i?"check":"refresh",size:17,color:i?n.success:n.subtle,strokeWidth:2.2}),e.jsxs("div",{style:{flex:1},children:[e.jsx("div",{style:{fontSize:13.5,fontWeight:700,color:i?n.successInk:n.text},children:i?`${d.length} authorization${d.length===1?"":"s"} confirmed`:u?"Checking authorization…":"Waiting for authorization"}),e.jsx("div",{title:p.join(", "),style:{marginTop:2,fontSize:12,color:i?n.successInk:n.muted},children:i?`${j} can access Orako.`:T?"Could not check right now. Your agent may still be connected.":"Authorize in your agent, then return to this tab."})]}),e.jsx("button",{type:"button",onClick:()=>{y(!0),m()},style:{border:`1px solid ${i?n.successBorder:n.borderStrong}`,background:n.surface,color:i?n.successInk:n.body,borderRadius:8,padding:"7px 10px",fontFamily:"inherit",fontSize:12,fontWeight:600,cursor:"pointer"},children:"Check again"})]})}export{ne as ConnectAgent,ae as ConnectAgentPage};
