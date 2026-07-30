// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// roleUser is the MCP prompt-message role every Orako prompt uses.
const roleUser = "user"

// serverInstructions is returned by `server/discover` to 2026-07-28 clients
// and in the `initialize` response to legacy clients. It is the ONLY guidance
// a fallback agent (one that connected via a per-agent picker or a generic
// remote-MCP snippet, without installing Orako's local skill) ever sees — it
// must be self-sufficient: what Orako is, the workflow loop, and the hard
// rules (search first, humans answer, contested hits are provisional). Kept
// in lockstep with cli/internal/mcp/server.go's serverInstructions (the stdio
// equivalent) so the two transports teach the same workflow.
const serverInstructions = `Orako MCP server — connect a coding agent to your team's experts and conversation history.

WHAT THIS IS: Orako routes a question your agent can't answer from its own tools to a human expert (product owner, senior dev, DevOps, whoever has the domain), and keeps every resolved answer in searchable conversation history. Orako never generates answers itself — it is LLM-free. Every answer that comes back through these tools was written by a person.

THE LOOP, IN ORDER:
1. search_history FIRST, always, before asking a human. It searches every past conversation (all statuses), so most questions were already asked once; searching is free and instant, asking a human costs their time.
2. BRANCH on each hit's "status" — this is how history routes you instead of blindly asking again:
   - "open" — the SAME question is already in flight. Do NOT open a duplicate: call add_participant to add the current asker to that thread and converge there.
   - "resolved" — reuse it. Call get_conversation on the hit's conversation_id for the full answer; if it looks stale or disputed, open a fresh follow-up ask instead of relying on it.
   - "timed_out" / "dismissed" — it was asked but never answered. Re-ping the original asker (the hit's asker_member_id) via ask, or group that asker with a fresh expert.
   - no hit — ask a fresh question (steps 3+).
3. On a genuine miss, call list_experts to see who is routable (by domains/expertise tags), then ask. Target one expert by ID when you know who owns the area; otherwise dispatch by domains to reach every matching teammate — all their replies land on the same thread, and the first answerer is labeled the expert. Route on expertise, never on presence — anyone contacted can answer at any moment. (Multi-project org? Confirm the right project with list_projects first — match the question to the codebase/topic, don't assume the default scope.) YOUR OWN USER is a routable expert too: when the person with the answer is the very human you work for and they are not responding in this session (away from the terminal, long-running autonomous work), ask THEM via ask with their member id — Orako reaches them on Discord/Slack/etc., their reply lands on the conversation, and you pick it up with get_conversation exactly like any other answer. Prefer that over stalling on an unanswered terminal question; note in the session that you routed the question to them.
4. Assemble a SELF-CONTAINED context packet before asking: files, symbols, ticket links, the relevant git history — pull all of it from your own tools (git, Jira, Confluence, whatever you have). Orako will not fetch or summarize context for you.
5. BEFORE you send, show the user a short recap and get their go-ahead: the PROJECT the question will post to (name it explicitly — never rely on silent auto-scoping; the user must see which project it lands in), the expert or pool you are targeting, and the exact question you will send. Do not open a conversation the user has not seen.
6. Asking is ASYNCHRONOUS — a human has to reply — but do NOT bail to "it's async, ping me when you want" just because an expert looks offline: presence is not a reliable signal, and anyone contacted can answer at any moment. Instead WAIT on an escalating ladder, keeping the user informed at each step, and never go silent. First, with wait=true, ask blocks briefly server-side for a live reply — announce "waiting for a live reply from {name}" BEFORE that call so the pause is explained; if it returns answered, relay it. If not, check the thread again with get_conversation at ~2 min, then ~5 min, then ~10 min (SPACE the checks — never sleep-poll in a tight loop). Only after that whole ladder comes up empty do you tell the user it is now parked async and that you will relay the answer once it lands (or they can say "check the answer" and you pull the latest). When get_conversation shows the OTHER party's messages are source=agent (another coding agent is on the thread), proactively offer the user to switch to sync mode (the orako_sync prompt) for a fast agent-to-agent exchange — propose it, and enter it only once they accept.
7. Use follow_up to keep the SAME thread going — challenge an answer, ask for more detail, or re-ask seriously after a joke / unclear / non-answer. Never open a second ask for a question you already have an open conversation on; that spawns a duplicate thread and re-pings the expert. An 'answered' conversation is still open until you resolve_conversation, so follow_up on it rather than starting over.
8. When resolved, call resolve_conversation with the DISTILLED resolution written in Markdown — this is what the next agent's search_history surfaces as a resolved hit. Orako does not write this for you; a vague or copy-pasted resolution degrades the history for everyone. Start the resolution with a one-line "## Heading" naming the topic — it becomes the conversation's title in history (otherwise the raw question is shown, which reads badly for chat-style questions). Pass a short "tags" list (facets like area, component, ticket) — they feed history search (keyword + facets) so future queries in different wording still find this conversation. If you know a second opinion was captured and it genuinely disagreed with the closing answer, set contested_set=true and contested=true so the resolved conversation is flagged provisional for future readers.

CONTRIBUTING KNOWLEDGE (optional, encouraged): when you learn something durable from your OWN work — a Linear ticket you resolved, a config fix you applied, the rationale behind a commit, a gotcha you solved — call suggest_knowledge to propose it. It lands PENDING for a human to approve in one click; it is NOT published and does NOT show up in search_history until then, so proposing freely is safe. This is how the knowledge base fills itself from real dev work. (add_knowledge, by contrast, is admin-only and publishes immediately.)

RULES THAT DO NOT BEND:
- search_history before every ask — no exceptions. Never ask a human a question you have not first looked for in the history, and branch on the hit's status (join an open thread, reuse a resolved one, re-ask a timed-out/dismissed asker) instead of opening a duplicate.
- Show the recap (project · expert/pool · the exact question) and get the user's go-ahead before every ask. Never open a conversation the user has not seen, and never hide which project it posts to.
- Presence is not a routing decision: never downgrade to "async, ping me later" up front because someone looks offline. Post, then work the wait ladder above.
- One topic, one thread. Never open a second conversation for a topic that already has an OPEN one: same person → follow_up; another person's take needed → add_participant on the existing thread (they get the history), then follow_up with what concerns them. Only a CLOSED earlier thread justifies a new conversation — seeded with its distilled resolution.
- Never fabricate an expert's answer. If a conversation is still open, say so and offer to check back — do not guess on the human's behalf.
- All LLM work (normalizing the question, extracting tags, distilling the resolution) is yours to do; Orako stores and routes, it does not generate.
- A tool call outside your project or role fails with a permission error — that is RBAC working as intended, not a bug to route around.`

// --- Tool description constants ---
//
// These are read by every MCP client on tools/list and are the primary
// steering signal for an agent that has no local skill installed. Kept
// textually aligned with cli/internal/mcp/tools.go's constants (same product,
// same contract) — the HTTP transport is a new delivery mechanism for
// identical guidance, not a new product surface.
const (
	descSearchHistory       = "Call this FIRST, before asking a human. Deterministic history search over the project's conversations across ALL statuses — no embeddings. Returns top-k {conversation_id, title, summary, status, tags, entities, asker_member_id, age_days, project}, plus known_tags/known_entities (the project's existing vocabulary — reuse those labels when you ask/resolve/follow_up). BRANCH on each hit's `status`: `open` = the same question is already in flight, do NOT duplicate — call add_participant to add the current asker to that thread and converge; `resolved` = reuse it, call get_conversation on the conversation_id for the full answer (re-ask if it looks stale/disputed); `timed_out` or `dismissed` = it was asked but never answered, re-ping the original asker_member_id via ask (or group that asker with a fresh expert); no hit = ask a fresh question. project_id is optional: omit it to search your connection's primary project."
	descListExperts         = "Return the expert directory (members and their expertise tags in `domains`) so the agent can match against its git history to pick the best expert. Every project member is routable — route on the `domains` tags, never on presence/'online' (an expert can answer a question at any time). Each entry also carries `delivery_channel` (where Orako reaches them: discord/slack/teams/telegram/dashboard), an optional provider-native `mention` (use it instead of a plain first name when directly addressing that person in a relayed message), and `max_message_chars`, an ADVISORY per-bubble size for that channel (0 = no practical limit, e.g. dashboard). Nothing is truncated: a longer question arrives as several sequential messages, split at paragraph boundaries. Staying within one bubble reads best, so keep questions concise and put the bulk in `context`."
	descAsk                 = "Opens a NEW conversation — only after a history miss, and only when no open thread on this TOPIC already exists (check list_conversations first). If you already asked this and the reply was a joke / unclear / incomplete, call follow_up on that conversation_id instead of opening a duplicate. Need ANOTHER person's take on a topic that already has an OPEN thread (e.g. the product side of a question a dev is already answering)? Do NOT open a second conversation — call add_participant on the existing thread (they receive the history) and follow_up with the part that concerns them. Only when the earlier thread is RESOLVED do you open a new one — and then seed its context with the resolved thread's distilled resolution (search_history will surface it). Target ONE expert (member_id) when an obvious expert exists; otherwise pass domains to reach every matching expert — all replies land on the thread and the first answer is what you're waiting on (exactly one of the two). Your OWN user is a valid direct target (self-ask): when THEY hold the answer but are not responding in this session, send the question to their member id instead of stalling — they answer from their chat platform and you collect it with get_conversation. Provide a self-contained context packet (files, tickets, symbols) assembled from your own tools — the server will not summarize. Write question and context in Markdown (lists, code fences) — humans read them in a dashboard. Set wait=true to block up to ~90s for a live reply (answered=true, inline_answer) — never emulate waiting with sleeps, and never decide up front that nobody is around: presence is NOT a reliable signal and an expert can answer at any moment. On a wait timeout you get conversation_id — do NOT bail to \"it's async, ping me later\"; keep the user informed and resume the ladder yourself with get_conversation at ~2 min, then ~5 min, then ~10 min (space the checks, never tight-loop). Pool asks return pool_size so you can report how many experts were reached. The response echoes project_name and recipient_names — use them to confirm where the question went (\"sent to Marc in cfea\") instead of repeating ids. project_id is optional when your connection is scoped to one project (it defaults to the primary); pass it explicitly when your connection spans several. To attach files or images to the opening question (a screenshot of the error, a diagram, a log), upload_attachment first and pass the returned ids in attachment_ids — delivered inline to a direct expert. Length limits: question up to 10,000 characters and context up to 100,000 — a longer body is rejected, so distil or link instead of pasting huge dumps."
	descGetConversation     = "Fetch a conversation's status and messages by conversation_id. This is how you work the wait ladder after an ask times out — poll it at ~2, then ~5, then ~10 min (space the checks, never tight-loop) — and how you pull the latest when the user says 'check the answer'. Returns the messages so far and whether it's still open. Each message carries a readable `author` formatted as name (member id), plus `source` (\"human\", \"agent\", or \"system\") and `agent_client`: if the OTHER party's recent messages are `source=agent`, you're talking to another coding agent — proactively OFFER the user to switch to sync mode (the orako_sync prompt) for a fast agent-to-agent back-and-forth, and only enter it once they accept."
	descFollowUp            = "Continue an EXISTING conversation on the same topic — this is how you re-ask when the reply was unclear, joking, or incomplete, challenge an answer, or ask for more detail. Use this, NOT ask, whenever you already have a thread with this expert about this question: it keeps the context and avoids opening a duplicate conversation (a fresh ask re-pings the expert with a brand-new thread). Needs the conversation_id (from ask / list_conversations / get_conversation). Works while the conversation is open — an 'answered' thread is still open until resolve_conversation, so you can keep pressing for a real answer. The follow-up message is limited to 10,000 characters. Set authored_by_human=true when you are relaying the USER's own words (they dictated it, or a /sync flow where the human wrote the reply) so it shows as the person, not 'via <your agent>'; leave it false when you composed the message yourself. To attach files or images to the reply, upload_attachment first and pass the returned ids in attachment_ids."
	descResolveConversation = "Resolve a conversation and record the answer on the thread. Provide the distilled resolution in Markdown; the server does not generate it. START it with a one-line `## Heading` that names the topic in a few words (e.g. `## Test vs production configuration`) — that heading becomes the conversation's TITLE in history; without it the raw (often chat-style, verbose) question is shown as the title, which reads badly. Then the distilled answer in short paragraphs, lists, code fences. This is what a future search_history surfaces as the resolved answer. Pass a short `tags` list (area, component, ticket) — they feed history search. If a second opinion was captured on this conversation, set contested_set=true and contested=true when it genuinely diverged from your resolution, or contested=false when it agreed; leave contested_set=false to let the server default. The resolution is limited to 10,000 characters."
	descListProjects        = "List the Orako projects the caller is a member of. Returns id, name, and the caller's role in each project. Use this to discover valid project_id values before calling project-scoped tools. When the org has more than one project, call this and pick the project the question actually belongs to (match it to the codebase/topic) — do not silently fall back to the default scope."
	descUploadAttachment    = "Upload a file or image to a conversation, then reference the returned attachment_id in attachment_ids on your next ask or follow_up so it is attached to that message. Orako delivers attachments to every participant on their own channel (Discord/dashboard/…) and stores them so anyone on the thread — including you, via get_conversation's download_url — can fetch them. Provide bytes_base64 (standard base64), a filename, and ideally a mime_type. Use it to send a diagram, a screenshot, a log file, or any artifact an expert needs to see; and when a human replies with an image, you read it back from get_conversation's download_url. There is a per-file size limit (a too-large file is rejected). Requires object storage configured on the server; if attachments are unavailable the call returns a clear error."
	descAddParticipant      = "Add another expert to an EXISTING conversation so they receive every question and answer on it (Orako proxies to each participant on their own channel — no group chat). Use this to loop a second expert into a thread you already opened, e.g. validate the technical side with one expert and the product side with another on the same conversation. Needs conversation_id (from ask / list_conversations) and the member_id to add (from list_experts). You must already be on the conversation to add someone; the added member must belong to the conversation's project. The newly added participant is delivered the thread so far. Idempotent: adding someone already on the conversation is a no-op (already_participant=true)."
	descAddKnowledge        = "ADMIN ONLY. Author curated knowledge — a team-maintained answer (FAQ entry, config fix, runbook) that becomes searchable via search_history exactly like a resolved conversation, but attributed to the org (\"Curated by …\") instead of a person, and excluded from responder analytics. Use it to SEED answers before any real conversation exists (e.g. the project's setup/config FAQ) so a future agent finds the answer instead of pinging a human. Provide {question, answer} and, like a good resolution, optional summary/tags/entities (same normalization as resolve_conversation) so it ranks well in search. For a batch import (many FAQ items at once) pass entries:[{question,answer,…}, …] instead of the single fields. The org is taken from your connection, never a payload field; entries are created org-wide. A non-admin caller is rejected — this is not the tool for asking a question (use ask) or recording a conversation's outcome (use resolve_conversation)."
	descSuggestKnowledge    = "PROPOSE knowledge from your own work — available to any member, no admin needed. Use it when YOU learn something durable while working: a Linear ticket you resolved, a config fix you applied, the rationale behind a commit, a gotcha you hit and solved. It creates a PENDING agent-suggested entry that a human reviews and approves in one click — it is NOT published and does NOT appear in search_history until approved, so an over-eager suggestion can never pollute the team's answers. Provide {question, answer} plus optional summary/tags/entities (same normalization as resolve_conversation) so it ranks well once approved. The org + author come from your connection, never a payload field. Contrast add_knowledge (admin, publishes an active entry immediately) and resolve_conversation (records the outcome of a human conversation): suggest_knowledge captures what your agent figured out on its own, for a human to vet."
	descListConversations   = "List conversations in a project, newest first, with their status. This is also STEP 1 of answering a ping: when the user was contacted on an Orako thread (a Discord/Slack DM pointing here) and wants to reply from this terminal, call this with status=\"open\", then get_conversation on the thread, draft a codebase-grounded reply, and send it with follow_up after the user approves. If the list comes back EMPTY, do not conclude there is nothing: the thread may live in another project — call list_projects and re-run this for the caller's other projects before giving up. Use it to resume or review threads and — before opening a new ask on a topic you've already raised — to find the EXISTING open thread and follow_up on it instead of creating a duplicate. The returned conversation_id values are valid inputs for get_conversation, follow_up, and resolve_conversation."
)

// promptSearchFirst is a reusable template that reminds the agent of the
// search-first rule before it drafts a question — useful for clients that
// surface MCP prompts as slash-command-like shortcuts.
const promptSearchFirst = `Before asking an expert, have you:
1. Called search_history with the clearest restatement of the question?
2. Branched on the top hit's status (open → add_participant; resolved → reuse via get_conversation; timed_out/dismissed → re-ask the asker) instead of asking again?
3. Tried at least one rephrasing if the first search came back empty?

If all three are done and it's still a miss, call list_experts, pick a target (by member ID or by domains), and call ask with a self-contained context packet (files, symbols, ticket links) written in Markdown.`

// promptResolveWithResolution reminds the agent what a good resolve_conversation
// call looks like — the resolution text is what gets promoted into the history.
const promptResolveWithResolution = `Before calling resolve_conversation, write the resolution as if it will be the ONLY thing a future agent sees for this question (it will, via search_history):
- Start with a one-line "## Heading" naming the topic in a few words — it becomes the conversation's title in history.
- Lead with the direct answer, not the back-and-forth that produced it.
- Use Markdown: short paragraphs, a list for multi-step answers, code fences for commands/config.
- If a second opinion on this conversation genuinely disagreed with this resolution, set contested_set=true and contested=true so the resolved conversation is flagged provisional.`

// promptAnswer helps an expert who was pinged on a conversation pull it back
// into their own agent and answer without leaving their terminal. Surfaced as a
// slash-command-like shortcut ("answer the last thing I was asked") on clients
// that expose MCP prompts. Named "answer" — NOT "resume" — because /resume is a
// built-in session command in several agent CLIs (and "résumé" reads as
// "summary" in French); the same flow is also steered from the
// list_conversations tool description for clients that expose only tools (e.g.
// Codex).
const promptAnswer = `You were pinged on an Orako conversation and want to answer from here, with your codebase context.

1. Call list_conversations with status="open" to find the thread(s) waiting on you (newest first). If it comes back EMPTY, don't stop: the thread may live in another project — call list_projects and re-run list_conversations for each other project.
2. If more than one thread is open, show the list and let the user pick; otherwise take the most recent.
3. Call get_conversation on that conversation_id to read the full question and context.
4. Draft the answer using THIS repo — cite the files, symbols, and decisions that actually settle it. You are the expert's agent: answer as if the expert is speaking, grounded in the code, not a generic guess.
5. Show the draft to the user and get a yes before sending. On yes, call follow_up with the answer (or resolve_conversation with a distilled resolution if this resolves it).

Your reply is delivered tagged as the expert's agent, so the asker sees it came through Orako, not typed by hand.`

// promptSync puts the agent into a BOUNDED live-collaboration loop for when two
// people are actively working a thread together in real time — the opt-in
// exception to the normal spaced 2/5/10-min wait ladder. It is deliberately
// bounded (a few minutes) so it never becomes an infinite tight-loop.
const promptSync = `Live "sync" mode — use ONLY when the user is actively collaborating on an open Orako thread in real time and wants quick back-and-forth (not the default; the normal flow spaces checks at ~2/5/10 min).

Loop, for at most ~5 minutes total:
1. Call get_conversation (or list_conversations status="open") to see if a new message arrived.
2. If there is a new message addressed to you, help the user draft a reply grounded in the codebase, confirm, and follow_up.
3. If nothing new, wait ~30 seconds and check again. Do NOT busy-wait — space each check by ~30s.
4. Stop after ~5 minutes (or 2–3 empty checks in a row) and tell the user the thread is quiet; they can re-invoke sync to keep going. Never loop indefinitely.

The other side can enter the same mode, so two agents can trade messages every ~30s while their humans stay in their terminals.`

// registerPrompts adds the optional reusable prompt templates. Prompts are a
// convenience surface (some MCP clients expose them as slash commands); the
// authoritative guidance is serverInstructions plus the tool descriptions
// above, which every client sees regardless of prompt support.
func registerPrompts(srv *sdkmcp.Server) {
	srv.AddPrompt(
		&sdkmcp.Prompt{
			Name:        "orako_search_first",
			Description: "Checklist to run before asking an expert: search the history, treat contested hits as provisional, then ask with self-contained context.",
		},
		func(_ context.Context, _ *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
			return &sdkmcp.GetPromptResult{
				Description: "Search-first checklist",
				Messages: []*sdkmcp.PromptMessage{
					{Role: roleUser, Content: &sdkmcp.TextContent{Text: promptSearchFirst}},
				},
			}, nil
		},
	)

	srv.AddPrompt(
		&sdkmcp.Prompt{
			Name:        "orako_resolve_with_resolution",
			Description: "Checklist for writing a resolve_conversation resolution that is genuinely reusable in the history.",
		},
		func(_ context.Context, _ *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
			return &sdkmcp.GetPromptResult{
				Description: "Resolve-with-resolution checklist",
				Messages: []*sdkmcp.PromptMessage{
					{Role: roleUser, Content: &sdkmcp.TextContent{Text: promptResolveWithResolution}},
				},
			}, nil
		},
	)

	srv.AddPrompt(
		&sdkmcp.Prompt{
			Name:        "orako_answer",
			Description: "Answer the open conversation you were pinged on: pull it into this agent and draft a codebase-grounded reply, without leaving your terminal.",
		},
		func(_ context.Context, _ *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
			return &sdkmcp.GetPromptResult{
				Description: "Answer the thread you were asked about",
				Messages: []*sdkmcp.PromptMessage{
					{Role: roleUser, Content: &sdkmcp.TextContent{Text: promptAnswer}},
				},
			}, nil
		},
	)

	srv.AddPrompt(
		&sdkmcp.Prompt{
			Name:        "orako_sync",
			Description: "Enter a bounded live-collaboration loop (check ~every 30s for a few minutes) for real-time back-and-forth on an open thread.",
		},
		func(_ context.Context, _ *sdkmcp.GetPromptRequest) (*sdkmcp.GetPromptResult, error) {
			return &sdkmcp.GetPromptResult{
				Description: "Live sync mode (bounded)",
				Messages: []*sdkmcp.PromptMessage{
					{Role: roleUser, Content: &sdkmcp.TextContent{Text: promptSync}},
				},
			}, nil
		},
	)
}
