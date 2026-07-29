// Maps an MCP client name (message.agentClient) to a display label and a small
// monogram badge, so an agent-authored message reads "Name · Claude Code" with
// the agent's mark. An unknown-but-present client gets a title-cased fallback;
// no client returns null (render just the name).

export interface AgentIdentity {
  label: string
  short: string // 1-2 char monogram for the badge
  bg: string
  fg: string
}

const CLAUDE: AgentIdentity = { label: 'Claude Code', short: 'C', bg: '#F3E8DD', fg: '#8A4B26' }
const CODEX: AgentIdentity = { label: 'Codex', short: 'Cx', bg: '#E4ECEF', fg: '#123A45' }
const CURSOR: AgentIdentity = { label: 'Cursor', short: 'Cu', bg: '#E8E9EC', fg: '#1D2430' }
const GEMINI: AgentIdentity = { label: 'Gemini', short: 'G', bg: '#E4EDFB', fg: '#1A4FA0' }

const KNOWN: Record<string, AgentIdentity> = {
  'claude-code': CLAUDE,
  claude: { ...CLAUDE, label: 'Claude' },
  codex: CODEX,
  'codex-cli': CODEX,
  // Codex CLI's MCP client declares itself "codex-mcp-client" (observed live).
  'codex-mcp-client': CODEX,
  cursor: CURSOR,
  gemini: GEMINI,
  'gemini-cli': GEMINI,
}

// FAMILIES catches undeclared variants ("claude-code-sdk", "cursor-agent", …):
// a client name containing the substring maps to that family's badge. Checked
// in order after the exact KNOWN lookup misses.
const FAMILIES: Array<[string, AgentIdentity]> = [
  ['claude', CLAUDE],
  ['codex', CODEX],
  ['cursor', CURSOR],
  ['gemini', GEMINI],
]

// agentIdentity resolves a client name to its badge, or null when absent.
export function agentIdentity(client?: string): AgentIdentity | null {
  const key = (client ?? '').trim().toLowerCase()
  if (!key) return null
  if (KNOWN[key]) return KNOWN[key]

  for (const [needle, identity] of FAMILIES) {
    if (key.includes(needle)) return identity
  }

  const label = key.replace(/[-_]+/g, ' ').replace(/\b\w/g, c => c.toUpperCase())
  return { label, short: (key[0] ?? 'A').toUpperCase(), bg: '#EDEFF2', fg: '#5C6470' }
}
