# Orako over Connect-JSON (headless / non-MCP)

For an agent that does **not** speak MCP. Every Orako tool is also a plain HTTP
endpoint: `POST {BASE}/orako.v1.OrakoService/{Method}` with a JSON body and your
machine token. No SDK required — `curl`, `fetch`, `requests`, anything.

- **`{BASE}`** — your Orako API base, e.g. `https://api.orako.io` (self-host: your
  own host).
- **Auth** — `Authorization: Bearer mcp_at_…` (an Orako machine token; an org admin
  mints it in the dashboard → Settings → Machine tokens; it is project-scoped and
  revocable).
- **Headers** — always `Content-Type: application/json`.
- **JSON field names are camelCase** (`project_id` → `projectId`, `conversation_id`
  → `conversationId`, `member_id` → `memberId`, `top_k` → `topK`).

Set once:

```bash
export ORAKO_BASE="https://api.orako.io"
export ORAKO_TOKEN="mcp_at_xxx"   # your machine token
orako() { curl -sS -X POST "$ORAKO_BASE/orako.v1.OrakoService/$1" \
  -H "Authorization: Bearer $ORAKO_TOKEN" -H "Content-Type: application/json" -d "$2"; }
```

## The four core operations

### 1. SearchHistory — ALWAYS first

```bash
orako SearchHistory '{"query":"activer les updates auto WordPress","topK":5}'
```
Optional: `"projectIds":["…"]` (default = every project the token can see),
`"status":"resolved"`, `"tags":["wordpress"]`.
Response: `{ "hits": [ { "conversationId","title","summary","status",
"askerMemberId","tags","entities","projectId",… } ], "knownTags":[…],
"knownEntities":[…] }`. **Branch on each hit's `status`** (resolved→reuse,
open→AddParticipant, timed_out/dismissed→re-ask, none→Ask).

### 2. Ask — only after a miss

Direct to one expert:
```bash
orako Ask '{
  "memberId":"<expert-member-id>",
  "question":"PushRank gère-t-il le multi-site WPML ?",
  "context":"## Contexte\nUtilisateur sur WordPress multisite…",
  "title":"Support WPML multi-site",
  "wait":true
}'
```
Or dispatch to a pool by expertise (omit `memberId`, pass `domains`):
```bash
orako Ask '{"domains":["integrations"],"question":"…","wait":true}'
```
Exactly one of `memberId` / `domains`. `projectId` is optional when the token
scopes one project; **required** when it scopes several. `wait:true` blocks ~90s
for a live reply.
Response: `{ "conversationId","answered","inlineAnswer","poolSize",
"projectName","recipientNames":[…] }`. If `answered:true`, use `inlineAnswer`.
Otherwise keep the `conversationId` and poll.

### 3. GetConversation — poll for the answer / read the thread

```bash
orako GetConversation '{"conversationId":"<id>"}'
```
Response: `{ "status":"open|answered|resolved|timed_out",
"messages":[{"authorMemberId","body","source","at",…}], "participants":[…] }`.
Work the ladder: after an `Ask` timeout, poll at **~2 min, ~5 min, ~10 min** —
space the checks, never tight-loop. `status` leaves `open`/`answered` when a human
has replied; read the latest `messages[].body` where `source:"human"`.

### 4. FollowUp — keep the same thread

```bash
orako FollowUp '{"conversationId":"<id>","body":"Merci — et pour le mode sous-domaine ?"}'
```
Use this (not a new `Ask`) to challenge, clarify, or re-ask on a thread you already
opened — a fresh `Ask` would spawn a duplicate and re-ping the expert.

## Minimal headless flow (pseudocode)

```
hits = SearchHistory(query)
if a hit is resolved:            reuse GetConversation(hit.conversationId)   # done, no human bothered
elif a hit is open:              AddParticipant(hit.conversationId, me); FollowUp(...)
else:
    r = Ask(question, context, memberId|domains, wait=true)
    if r.answered:  use r.inlineAnswer
    else:
        for delay in [120s, 300s, 600s]:
            sleep(delay)
            c = GetConversation(r.conversationId)
            if c has a human reply: use it; break
        else: park — continue other work, relay the answer when it lands
# when settled:
ResolveConversation(conversationId, "## Heading\n<distilled markdown answer>", tags=[…])
```

## Also available (same pattern)

`ListExperts` (who is routable, by `domains`), `ListProjects`, `ListConversations`
(`{"status":"open"}` to find threads waiting on you), `AddParticipant`
(`{"conversationId","memberId"}`), `ResolveConversation`
(`{"conversationId","resolution","tags":[…]}`). A call outside your token's
project/role returns a permission error — that is RBAC, not a bug.

> The policy (when to ask, the search-first loop, one-topic-one-thread) is in
> `SKILL.md`. This file is only the transport detail.
