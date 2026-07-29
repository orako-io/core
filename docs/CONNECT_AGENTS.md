# Connect an agent

Orako exposes a remote MCP server at:

```text
https://orako.example.com/mcp
```

Replace the host with your `ORAKO_BASE_URL`. The same endpoint works for Claude
Code, Codex, Cursor, Gemini CLI, and other clients that support remote MCP over
HTTP.

## Interactive MCP clients

1. Add the remote MCP URL in your client's MCP settings.
2. Start the authorization flow from the client.
3. Sign in to Orako if needed.
4. Choose the organization and projects the client may access.
5. Approve the connection.

An existing Orako session is reused. There is no join code in the MCP login
flow: join links add a person to an organization, while MCP authorization grants
an already signed-in member's client access to selected projects.

Client menus and commands change frequently. The current per-client setup
instructions live at [orako.io/agents](https://orako.io/agents).

## Verify the connection

Ask the client to list its Orako projects or experts. A connected client should
expose tools including:

- `ListProjects`
- `ListExperts`
- `SearchHistory`
- `Ask`
- `GetConversation`
- `FollowUp`
- `AddParticipant`
- `ResolveConversation`

Before opening a new question, the agent should search history. For the complete
policy, install or copy the files in
[`pack/orako-ask-a-human`](../pack/orako-ask-a-human/).

## Headless and non-MCP agents

For CI jobs, autonomous workers, and frameworks without MCP:

1. An organization admin creates a project-scoped machine token in
   **Settings → Machine tokens**.
2. The worker sends the token as `Authorization: Bearer mcp_at_…`.
3. It calls the Connect-JSON endpoints documented in
   [`pack/orako-ask-a-human/references/connect-json.md`](../pack/orako-ask-a-human/references/connect-json.md).

Machine tokens are long-lived credentials. Store them in a secret manager,
scope them only to required projects, and revoke them when the worker is
retired.

## Troubleshooting

### The client opens the dashboard but cannot authorize

- Confirm `ORAKO_BASE_URL` is the public HTTPS origin with no trailing path.
- Confirm the reverse proxy forwards the original host and scheme.
- Confirm `/mcp` and the OAuth routes reach the same Orako server.
- Confirm the signed-in account is a live member of at least one organization.

### The tools exist but the agent uses them poorly

Some clients expose MCP tools but ignore the server-level MCP instructions.
Add the appropriate policy from `pack/orako-ask-a-human` to `CLAUDE.md`,
`AGENTS.md`, or the agent's system prompt.

### A project is missing

Reconnect and approve that project, or revoke the existing connection under
**Settings → Connections** and authorize it again. Project access is explicit;
Orako does not silently grant newly created projects to an old connection.
