// Connect agent — choice-first flow (implements the "Connect Agent" design):
// (1) pick a lane (terminal/IDE vs no-terminal GUI), (2) pick a client,
// (3) one tailored panel, (4) client-specific guidance, (5) a test prompt. The MCP endpoint is
// {origin}/mcp so on-prem deployments work unchanged; OAuth happens inside
// the agent, no token to copy.

import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, type McpConnection } from '../lib/client'
import { useToast } from '../lib/toast'
import { Page } from '../components/Layout'
import { Icon } from '../components/Icon'
import { Select } from '../components/Select'
import { T } from '../lib/theme'
import agentsGuidance from '../../../pack/orako-ask-a-human/variants/AGENTS.md?raw'
import connectJsonGuidance from '../../../pack/orako-ask-a-human/references/connect-json.md?raw'
import skillGuidance from '../../../pack/orako-ask-a-human/SKILL.md?raw'
import systemPromptGuidance from '../../../pack/orako-ask-a-human/variants/system-prompt.txt?raw'
import skillArchiveUrl from '../../../pack/dist/orako-ask-a-human.skill?url'

// Lane A — terminal/IDE clients. Each carries the per-client remote-MCP
// registration (command form for CLIs, a JSON file for editor clients) plus the
// client's own authorize step. Orako is a remote MCP server; there is no CLI to
// install.
interface CliClient {
  label: string
  json?: string // set for clients configured by a file rather than a command
  path?: string // config file path, shown above a JSON snippet
  register: (url: string) => string
  authorize: string
  note?: string
}

const LANE_A: Record<string, CliClient> = {
  'claude-code': {
    label: 'Claude Code',
    register: url => `claude mcp add --transport http orako ${url}`,
    authorize: 'Run /mcp inside Claude Code to approve.',
    note: 'Usage: tools are called from natural language, and the Orako prompts also appear as slash commands — /mcp__orako__orako_answer (reply to the thread you were pinged on) and /mcp__orako__orako_sync (live back-and-forth). Reconnect /mcp after a server update to refresh them.',
  },
  codex: {
    label: 'Codex',
    register: url => `codex mcp add orako --url ${url}`,
    authorize: 'Run codex mcp login orako to approve.',
    note: 'Usage: Codex has no per-tool slash commands — drive Orako in natural language, e.g. “list my open orako conversations, then reply to the latest via follow_up: <message>”. Type /mcp in the Codex TUI to check the connection; if a thread doesn’t show up, ask it to check your other projects (list_projects).',
  },
  gemini: {
    label: 'Gemini CLI',
    register: url => `gemini mcp add -t http orako ${url}`,
    authorize: 'Run /mcp auth orako to approve.',
  },
  copilot: {
    label: 'GitHub Copilot (CLI)',
    register: url => `copilot mcp add --transport http orako ${url}`,
    authorize: 'Run copilot -i /mcp to approve.',
  },
  cursor: {
    label: 'Cursor',
    json: '.cursor/mcp.json',
    path: '.cursor/mcp.json',
    register: url => `{
  "mcpServers": {
    "orako": { "type": "http", "url": "${url}" }
  }
}`,
    authorize: 'Approve in the browser — Cursor prompts you automatically.',
  },
  vscode: {
    label: 'VS Code',
    json: '.vscode/mcp.json',
    path: '.vscode/mcp.json',
    register: url => `{
  "servers": {
    "orako": { "type": "http", "url": "${url}" }
  }
}`,
    authorize: 'Approve in the browser — VS Code prompts you automatically.',
  },
  windsurf: {
    label: 'Windsurf',
    json: '~/.codeium/windsurf/mcp_config.json',
    path: '~/.codeium/windsurf/mcp_config.json',
    register: url => `{
  "mcpServers": {
    "orako": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "${url}"]
    }
  }
}`,
    authorize: 'Approve in the browser — auto-prompted on first use.',
    note: 'Uses the generic mcp-remote npm proxy (not an Orako binary). Newer Windsurf builds have a native serverUrl field — prefer it if your version offers it.',
  },
}
const ORDER_A = ['claude-code', 'codex', 'gemini', 'copilot', 'cursor', 'vscode', 'windsurf']

// Lane B — no terminal, no config file. Each connects a remote MCP server
// from the app's own settings (paste the URL, click connect, approve in the
// browser). Steps verified against each vendor's help docs as of 2026-07.
interface GuiClient {
  label: string
  steps: string[]
  plan: string
}

const LANE_B: Record<string, GuiClient> = {
  'claude-desktop': {
    label: 'Claude Desktop',
    steps: [
      'Open Claude Desktop → profile picture → Settings → Connectors.',
      'Click "Add" next to Connectors.',
      'Paste the MCP URL above into the URL field and click "Add".',
      'Click "Connect" and approve access in the browser tab that opens.',
    ],
    plan: 'Available on Free, Pro, Max, Team and Enterprise (Free is limited to one custom connector). Claude reaches your server over the public internet, so it needs a publicly reachable deployment.',
  },
  'claude-ai': {
    label: 'Claude.ai (web)',
    steps: [
      'Open claude.ai → Settings → Customize → Connectors.',
      'Click the "+" next to Connectors, then "Add custom connector".',
      'Paste the MCP URL above into the URL field and click "Add".',
      'Click "Connect" and approve access in the browser tab that opens.',
    ],
    plan: 'Same Connectors UI as Claude Desktop, on Free, Pro, Max, Team and Enterprise (Free is limited to one custom connector).',
  },
  chatgpt: {
    label: 'ChatGPT',
    steps: [
      'Open ChatGPT → Plugins → Developer mode.',
      'Enable Developer Mode (first time only).',
      'Open Plugins → Browse Plugins, then click "+".',
      'Name the connector, paste the MCP URL above, then connect and approve access.',
    ],
    plan: "Requires a paid plan (Plus, Pro, Business, Enterprise or Edu). OpenAI flags Developer-mode connectors as able to take real actions on your behalf, so only connect servers you trust.",
  },
}
const ORDER_B = ['claude-desktop', 'claude-ai', 'chatgpt']

type GuidanceFormat = 'skill' | 'agents' | 'mcp'

const GUIDANCE_FORMAT_BY_CLIENT: Record<string, GuidanceFormat> = {
  'claude-code': 'skill',
  codex: 'agents',
  gemini: 'agents',
  copilot: 'agents',
  cursor: 'agents',
  vscode: 'agents',
  windsurf: 'agents',
}

const cardStyle: React.CSSProperties = {
  background: T.surface,
  border: `1px solid ${T.border}`,
  borderRadius: T.rXl,
  overflow: 'hidden',
}

const darkBlock: React.CSSProperties = {
  background: '#1B1F2A',
  borderRadius: 11,
  padding: '14px 16px',
}

const codeStyle: React.CSSProperties = {
  fontFamily: T.mono,
  fontSize: 13,
  color: '#E4E7EC',
}

const microLabel: React.CSSProperties = {
  fontSize: 11.5,
  fontWeight: 600,
  letterSpacing: '.03em',
  color: T.subtle,
}

const guidanceActionStyle: React.CSSProperties = {
  border: `1px solid ${T.accentBorder}`,
  background: T.surface,
  color: T.accentHover,
  borderRadius: 9,
  padding: '9px 12px',
  display: 'inline-flex',
  alignItems: 'center',
  gap: 7,
  fontFamily: 'inherit',
  fontSize: 12.5,
  fontWeight: 650,
  cursor: 'pointer',
  textDecoration: 'none',
}

function CopyIconBtn({ onCopy }: { onCopy: () => void }) {
  return (
    <button
      type="button"
      onClick={onCopy}
      title="Copy"
      style={{ border: 'none', background: 'transparent', padding: 0, cursor: 'pointer', color: T.subtle, display: 'inline-flex', flex: 'none' }}
    >
      <Icon name="copy" size={15} />
    </button>
  )
}

function GuidanceButton({
  children,
  icon,
  onClick,
}: {
  children: React.ReactNode
  icon: 'copy' | 'download'
  onClick?: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={guidanceActionStyle}
    >
      <Icon name={icon} size={14} strokeWidth={1.9} />
      {children}
    </button>
  )
}

function GuidancePreview({ children }: { children: string }) {
  return (
    <pre
      style={{
        ...darkBlock,
        ...codeStyle,
        margin: 0,
        maxHeight: 150,
        overflow: 'hidden',
        whiteSpace: 'pre-wrap',
        lineHeight: 1.55,
        maskImage: 'linear-gradient(to bottom, black 65%, transparent 100%)',
        WebkitMaskImage: 'linear-gradient(to bottom, black 65%, transparent 100%)',
      }}
    >
      {children}
    </pre>
  )
}

// StepHead — the numbered section marker for the outer flow (1–5).
function StepHead({ n, title, mt = 26 }: { n: number; title: string; mt?: number }) {
  return (
    <div style={{ marginTop: mt, display: 'flex', alignItems: 'center', gap: 10 }}>
      <span
        style={{
          width: 24,
          height: 24,
          borderRadius: '50%',
          background: T.accentSoft,
          color: T.accent,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          fontSize: 13,
          fontWeight: 700,
          flex: 'none',
        }}
      >
        {n}
      </span>
      <span style={{ fontSize: 15.5, fontWeight: 600, color: T.body }}>{title}</span>
    </div>
  )
}

function AmberNote({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start', background: T.warnBg, border: `1px solid ${T.warnBorder}`, borderRadius: 11, padding: '12px 14px' }}>
      <Icon name="alertTriangle" size={16} color={T.warn} strokeWidth={1.9} style={{ flex: 'none', marginTop: 1 }} />
      <p style={{ fontSize: 12.5, lineHeight: 1.55, color: T.warn, margin: 0 }}>{children}</p>
    </div>
  )
}

// LaneCard — one of the two "how do you connect?" choices (radio semantics).
function LaneCard({
  active,
  onSelect,
  title,
  desc,
  badge,
}: {
  active: boolean
  onSelect: () => void
  title: string
  desc: string
  badge?: string
}) {
  return (
    <button
      onClick={onSelect}
      style={{
        textAlign: 'left',
        cursor: 'pointer',
        padding: '16px 17px',
        borderRadius: 14,
        background: T.surface,
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        transition: 'all .15s',
        border: active ? `2px solid ${T.accent}` : `1px solid ${T.borderStrong}`,
        boxShadow: active ? T.shadowPop : 'none',
        fontFamily: 'inherit',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
        <span
          style={{
            width: 18,
            height: 18,
            borderRadius: '50%',
            flex: 'none',
            border: active ? `5px solid ${T.accent}` : '2px solid #C7CBD4',
            background: active ? T.accent : 'transparent',
          }}
        />
        <span style={{ fontSize: 14.5, fontWeight: 700, color: T.text }}>{title}</span>
        {badge && (
          <span
            style={{
              marginLeft: 'auto',
              fontSize: 10.5,
              fontWeight: 700,
              letterSpacing: '.04em',
              color: T.accentHover,
              background: T.accentSoft,
              border: `1px solid ${T.accentBorder}`,
              padding: '3px 7px',
              borderRadius: 6,
            }}
          >
            {badge}
          </span>
        )}
      </div>
      <p style={{ fontSize: 13, lineHeight: 1.5, color: T.muted, paddingLeft: 27, margin: 0 }}>{desc}</p>
    </button>
  )
}

// Standalone settings page: header + the reusable choice-first body below.
export function ConnectAgentPage() {
  return (
    <Page width={760}>
      <h2 style={{ fontSize: 27, fontWeight: 700, letterSpacing: '-.025em', color: '#12141B' }}>
        Connect your agent to Orako
      </h2>
      <p style={{ fontSize: 15, lineHeight: 1.6, color: T.muted, marginTop: 9, maxWidth: 640 }}>
        Point your coding agent — or Claude Desktop, ChatGPT — at Orako and authorize once. It gets your team's
        knowledge and teammates on tap. You authorize inside the agent; there's no token to copy.
      </p>
      <ConnectAgent />
    </Page>
  )
}

// ConnectAgent is the reusable choice-first body: lane toggle, client picker,
// one tailored panel, and a "did it work?" note. Rendered standalone by
// ConnectAgentPage and embedded as step 3 of the onboarding wizard
// (OnboardingPage) — one source of truth.
export function ConnectAgent() {
  const toast = useToast()

  const [lane, setLane] = useState<'terminal' | 'noterminal'>('terminal')
  const [clientT, setClientT] = useState('claude-code')
  const [clientB, setClientB] = useState('claude-desktop')
  const [headlessGuidance, setHeadlessGuidance] = useState(false)
  const [authorized, setAuthorized] = useState(false)

  const serverOrigin = window.location.origin
  const mcpUrl = `${serverOrigin}/mcp`

  const isTerminal = lane === 'terminal'
  const clientA = LANE_A[clientT] ?? LANE_A['claude-code']
  const clientGui = LANE_B[clientB] ?? LANE_B['claude-desktop']
  const clientLabel = isTerminal ? clientA.label : clientGui.label
  const guidanceFormat = isTerminal ? (GUIDANCE_FORMAT_BY_CLIENT[clientT] ?? 'agents') : 'mcp'

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      toast.success('Copied')
    } catch {
      toast.error('Copy failed')
    }
  }

  return (
    <>
      {/* STEP 1 — lane */}
      <StepHead n={1} title="How do you want to connect?" mt={34} />
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14, marginTop: 14 }}>
        <LaneCard
          active={isTerminal}
          onSelect={() => setLane('terminal')}
          title="In my terminal / IDE"
          badge="RECOMMENDED"
          desc="For developers. The CLI registers the remote server for you."
        />
        <LaneCard
          active={!isTerminal}
          onSelect={() => setLane('noterminal')}
          title="No terminal"
          desc="Claude Desktop, Claude.ai, ChatGPT — connect from the app's settings with a URL."
        />
      </div>

      {/* STEP 2 — client */}
      <StepHead n={2} title="Which client?" />
      <div style={{ marginTop: 13, maxWidth: 340 }}>
        {isTerminal ? (
          <Select value={clientT} onChange={e => setClientT(e.target.value)} style={{ height: 46, fontWeight: 600 }}>
            {ORDER_A.map(id => (
              <option key={id} value={id}>
                {LANE_A[id].label}
              </option>
            ))}
          </Select>
        ) : (
          <Select value={clientB} onChange={e => setClientB(e.target.value)} style={{ height: 46, fontWeight: 600 }}>
            {ORDER_B.map(id => (
              <option key={id} value={id}>
                {LANE_B[id].label}
              </option>
            ))}
          </Select>
        )}
      </div>

      {/* STEP 3 — tailored panel */}
      <StepHead n={3} title={`Set up ${clientLabel}`} />

      {isTerminal ? (
        <div style={{ ...cardStyle, marginTop: 14 }}>
          <div style={{ padding: '16px 20px', background: T.surfaceAlt, borderBottom: `1px solid ${T.borderSubtle}`, display: 'flex', gap: 11, alignItems: 'flex-start' }}>
            <Icon name="zap" size={18} color={T.accent} strokeWidth={1.9} style={{ flex: 'none', marginTop: 1 }} />
            <p style={{ fontSize: 13.5, lineHeight: 1.55, color: '#3A414D', margin: 0 }}>
              Register Orako&rsquo;s MCP URL, then approve access in {clientLabel}. The tools are available
              immediately after authorization.
            </p>
          </div>

          <div style={{ padding: '22px 22px 24px', display: 'flex', flexDirection: 'column', gap: 18 }}>
            <div>
              <div style={{ ...microLabel, marginBottom: 7 }}>
                REGISTER
                {clientA.path && (
                  <>
                    {' · '}
                    <span style={{ fontFamily: T.mono, color: T.muted, letterSpacing: 0 }}>{clientA.path}</span>
                  </>
                )}
              </div>
              <div style={{ ...darkBlock, display: 'flex', alignItems: 'flex-start', gap: 12 }}>
                <code style={{ ...codeStyle, fontSize: 12.5, flex: 1, overflowX: 'auto', whiteSpace: clientA.json ? 'pre' : 'nowrap', lineHeight: 1.6 }}>
                  {!clientA.json && <span style={{ color: T.subtle }}>$ </span>}
                  {clientA.register(mcpUrl)}
                </code>
                <CopyIconBtn onCopy={() => void copy(clientA.register(mcpUrl))} />
              </div>
              {clientA.note && <p style={{ fontSize: 12, lineHeight: 1.5, color: T.faint, margin: '8px 0 0' }}>{clientA.note}</p>}

              <div style={{ ...microLabel, margin: '16px 0 7px' }}>AUTHORIZE</div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, background: T.bg, border: `1px solid ${T.borderSubtle}`, borderRadius: 10, padding: '11px 14px' }}>
                <Icon name="lock" size={16} color={T.accent} strokeWidth={1.9} style={{ flex: 'none' }} />
                <span style={{ fontSize: 13.5, color: '#3A414D' }}>{clientA.authorize}</span>
              </div>
            </div>

            <AmberNote>
              MCP always exposes the tools. Some clients also relay Orako's server instructions to the model; the
              next step reinforces the workflow where that behavior is not guaranteed.
            </AmberNote>
          </div>
        </div>
      ) : (
        <div style={{ ...cardStyle, marginTop: 14 }}>
          <div style={{ padding: '16px 20px', background: T.surfaceAlt, borderBottom: `1px solid ${T.borderSubtle}`, display: 'flex', gap: 11, alignItems: 'flex-start' }}>
            <Icon name="shield" size={18} color={T.accent} strokeWidth={1.9} style={{ flex: 'none', marginTop: 1 }} />
            <p style={{ fontSize: 13.5, lineHeight: 1.55, color: '#3A414D', margin: 0 }}>
              <strong style={{ color: T.text }}>No install, no config file.</strong> These apps add a remote MCP
              server from their own settings: paste one URL, connect, approve in your browser. You get Orako's tools
              and their built-in descriptions.
            </p>
          </div>

          <div style={{ padding: 22, display: 'flex', flexDirection: 'column', gap: 20 }}>
            <div>
              <div style={{ ...microLabel, marginBottom: 7 }}>THE ONLY URL — SAME EVERYWHERE</div>
              <div style={{ ...darkBlock, display: 'flex', alignItems: 'center', gap: 12 }}>
                <code style={{ ...codeStyle, fontSize: 13.5, flex: 1, overflowX: 'auto', whiteSpace: 'nowrap' }}>{mcpUrl}</code>
                <CopyIconBtn onCopy={() => void copy(mcpUrl)} />
              </div>
            </div>

            <div>
              <div style={{ fontSize: 14.5, fontWeight: 600, color: T.body, marginBottom: 12 }}>Steps in {clientLabel}</div>
              <div style={{ display: 'flex', flexDirection: 'column' }}>
                {clientGui.steps.map((step, i) => (
                  <div key={i} style={{ display: 'flex', gap: 13, alignItems: 'flex-start', paddingBottom: 14 }}>
                    <span
                      style={{
                        width: 24,
                        height: 24,
                        borderRadius: 7,
                        background: T.accentSoft,
                        color: T.accent,
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        fontSize: 12.5,
                        fontWeight: 700,
                        flex: 'none',
                      }}
                    >
                      {i + 1}
                    </span>
                    <span style={{ fontSize: 13.5, lineHeight: 1.5, color: '#3A414D', paddingTop: 2 }}>{step}</span>
                  </div>
                ))}
              </div>
            </div>

            <AmberNote>
              {clientGui.plan} The exact menu wording and plan requirement change often — if your plan can't add a
              custom connector, you'll see it there rather than hunting.
            </AmberNote>
          </div>
        </div>
      )}

      <McpConnectionStatus resource={mcpUrl} onStatusChange={setAuthorized} />

      {/* STEP 4 — teach the loop */}
      {!authorized && (
        <div style={{ marginTop: 14, padding: '12px 15px', borderRadius: 12, border: `1px dashed ${T.borderStrong}`, background: T.surfaceAlt, color: T.muted, fontSize: 13, lineHeight: 1.5 }}>
          The optional agent playbook appears after Orako confirms at least one authorization.
        </div>
      )}
      <div style={{ display: authorized ? 'contents' : 'none' }} aria-hidden={!authorized}>
      <StepHead n={4} title="Teach your agent the Orako loop" />
      <div style={{ ...cardStyle, marginTop: 14 }}>
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: '1fr 1fr',
            background: T.surfaceAlt,
            borderBottom: `1px solid ${T.borderSubtle}`,
          }}
        >
          <div style={{ padding: '15px 18px', borderRight: `1px solid ${T.borderSubtle}` }}>
            <div style={{ ...microLabel, color: T.accent }}>TOOLS</div>
            <div style={{ fontSize: 13.5, fontWeight: 650, color: T.body, marginTop: 4 }}>Automatic over MCP</div>
            <p style={{ fontSize: 12, lineHeight: 1.5, color: T.muted, margin: '4px 0 0' }}>
              Search, ask, follow up, and resolve are available after authorization.
            </p>
          </div>
          <div style={{ padding: '15px 18px' }}>
            <div style={{ ...microLabel, color: T.accent }}>PLAYBOOK</div>
            <div style={{ fontSize: 13.5, fontWeight: 650, color: T.body, marginTop: 4 }}>Client-dependent</div>
            <p style={{ fontSize: 12, lineHeight: 1.5, color: T.muted, margin: '4px 0 0' }}>
              This locks in search-first, one-thread, and distilled resolution.
            </p>
          </div>
        </div>

        <div style={{ padding: 22 }}>
          <div
            role="group"
            aria-label="Agent guidance format"
            style={{
              display: 'inline-flex',
              padding: 3,
              gap: 3,
              borderRadius: 10,
              background: T.bg,
              border: `1px solid ${T.borderSubtle}`,
              marginBottom: 18,
            }}
          >
            {[
              { active: !headlessGuidance, label: `For ${clientLabel}`, onClick: () => setHeadlessGuidance(false) },
              { active: headlessGuidance, label: 'Custom / headless', onClick: () => setHeadlessGuidance(true) },
            ].map(option => (
              <button
                key={option.label}
                type="button"
                onClick={option.onClick}
                aria-pressed={option.active}
                style={{
                  border: 'none',
                  borderRadius: 7,
                  padding: '7px 11px',
                  background: option.active ? T.surface : 'transparent',
                  boxShadow: option.active ? T.shadowCard : 'none',
                  color: option.active ? T.text : T.muted,
                  fontFamily: 'inherit',
                  fontSize: 12.5,
                  fontWeight: option.active ? 650 : 500,
                  cursor: 'pointer',
                }}
              >
                {option.label}
              </button>
            ))}
          </div>

          {headlessGuidance ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <div>
                <div style={{ fontSize: 15, fontWeight: 700, color: T.text }}>Paste into your system prompt</div>
                <p style={{ fontSize: 13, lineHeight: 1.55, color: T.muted, margin: '5px 0 0', maxWidth: 620 }}>
                  For Hermes, CI, or a custom agent loop. If it does not speak MCP, also copy the Connect-JSON recipes
                  and mint a scoped token in <a href="/machine-tokens" style={{ color: T.accentHover }}>Settings → Machine tokens</a>.
                </p>
              </div>
              <GuidancePreview>{systemPromptGuidance}</GuidancePreview>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 9 }}>
                <GuidanceButton icon="copy" onClick={() => void copy(systemPromptGuidance)}>
                  Copy system prompt
                </GuidanceButton>
                <GuidanceButton icon="copy" onClick={() => void copy(connectJsonGuidance)}>
                  Copy Connect-JSON recipes
                </GuidanceButton>
              </div>
            </div>
          ) : guidanceFormat === 'skill' ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontSize: 15, fontWeight: 700, color: T.text }}>Already delivered by Claude Code</span>
                  <span style={{ ...microLabel, color: T.accent, background: T.accentSoft, borderRadius: 5, padding: '3px 6px' }}>OPTIONAL</span>
                </div>
                <p style={{ fontSize: 13, lineHeight: 1.55, color: T.muted, margin: '5px 0 0', maxWidth: 620 }}>
                  Claude Code relays Orako's MCP instructions. Install the skill only if you want stronger triggering
                  when a task reaches a human-only decision.
                </p>
              </div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 9 }}>
                <GuidanceButton icon="copy" onClick={() => void copy(skillGuidance)}>
                  Copy SKILL.md
                </GuidanceButton>
                <a href={skillArchiveUrl} download="orako-ask-a-human.skill" style={guidanceActionStyle}>
                  <Icon name="download" size={14} strokeWidth={1.9} />
                  Download .skill
                </a>
              </div>
            </div>
          ) : guidanceFormat === 'agents' ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              <div>
                <div style={{ fontSize: 15, fontWeight: 700, color: T.text }}>Add this to your project's AGENTS.md</div>
                <p style={{ fontSize: 13, lineHeight: 1.55, color: T.muted, margin: '5px 0 0', maxWidth: 620 }}>
                  MCP provides the tools. Project guidance guarantees the search-first workflow even when {clientLabel}{' '}
                  does not relay the server's instruction block.
                </p>
              </div>
              <GuidancePreview>{agentsGuidance}</GuidancePreview>
              <div>
                <GuidanceButton icon="copy" onClick={() => void copy(agentsGuidance)}>
                  Copy AGENTS.md guidance
                </GuidanceButton>
              </div>
            </div>
          ) : (
            <div>
              <div style={{ fontSize: 15, fontWeight: 700, color: T.text }}>No extra file to install</div>
              <p style={{ fontSize: 13, lineHeight: 1.55, color: T.muted, margin: '5px 0 0', maxWidth: 620 }}>
                {clientLabel} receives the Orako tools and their descriptions through MCP. Use the custom/headless tab
                only if your client gives you a place to add persistent instructions.
              </p>
            </div>
          )}

        </div>
      </div>

      {/* STEP 5 — did it work */}
      <StepHead n={5} title="Did it work?" />
      <div style={{ marginTop: 13, background: T.accentSofter, border: `1px solid ${T.accentBorder}`, borderRadius: 14, padding: '16px 18px', display: 'flex', gap: 12, alignItems: 'flex-start' }}>
        <Icon name="message" size={19} color={T.accent} strokeWidth={1.9} style={{ flex: 'none', marginTop: 1 }} />
        <p style={{ fontSize: 13.5, lineHeight: 1.6, color: '#3A414D', margin: 0 }}>
          There's nothing to test here — Orako runs inside your agent, not the dashboard. Open your agent and ask it
          to use an Orako tool: <em style={{ color: T.accentHover }}>"search Orako for the refund policy"</em> or{' '}
          <em style={{ color: T.accentHover }}>"list the teammates on this project."</em> If it answers, you're
          connected. If it says the server needs authentication, run its authorize step (e.g.{' '}
          <code style={{ fontFamily: T.mono, fontSize: 12, background: T.accentSoft, color: T.accentHover, padding: '1px 6px', borderRadius: 5 }}>/mcp</code>{' '}
          in Claude Code).
        </p>
      </div>
      </div>

    </>
  )
}

function McpConnectionStatus({
  resource,
  onStatusChange,
}: {
  resource: string
  onStatusChange?: (connected: boolean) => void
}) {
  const [connections, setConnections] = useState<McpConnection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)

  const load = useCallback(async () => {
    setError(false)
    try {
      const result = await api.listMcpConnections()
      setConnections((result.connections ?? []).filter(connection => connection.resource === resource))
    } catch {
      setError(true)
    } finally {
      setLoading(false)
    }
  }, [resource])

  useEffect(() => {
    void load()
    window.addEventListener('focus', load)
    return () => window.removeEventListener('focus', load)
  }, [load])

  useEffect(() => {
    onStatusChange?.(connections.length > 0)
  }, [connections.length, onStatusChange])

  const sorted = useMemo(
    () => [...connections].sort((a, b) => (b.connectedAt ?? '').localeCompare(a.connectedAt ?? '')),
    [connections],
  )
  const names = useMemo(
    () => [...new Set(sorted.map(connection => connection.clientName || 'MCP client'))],
    [sorted],
  )
  const connectionSummary = names.length > 1 ? `${names[0]} and ${names.length - 1} more` : names[0]
  const hasConnection = sorted.length > 0

  return (
    <div
      style={{
        marginTop: 14,
        border: `1px solid ${hasConnection ? T.successBorder : T.border}`,
        background: hasConnection ? T.successBg : T.surface,
        borderRadius: 12,
        padding: '13px 15px',
        display: 'flex',
        alignItems: 'center',
        gap: 11,
      }}
    >
      <Icon
        name={hasConnection ? 'check' : 'refresh'}
        size={17}
        color={hasConnection ? T.success : T.subtle}
        strokeWidth={2.2}
      />
      <div style={{ flex: 1 }}>
        <div style={{ fontSize: 13.5, fontWeight: 700, color: hasConnection ? T.successInk : T.text }}>
          {hasConnection ? `${sorted.length} authorization${sorted.length === 1 ? '' : 's'} confirmed` : loading ? 'Checking authorization…' : 'Waiting for authorization'}
        </div>
        <div title={names.join(', ')} style={{ marginTop: 2, fontSize: 12, color: hasConnection ? T.successInk : T.muted }}>
          {hasConnection
            ? `${connectionSummary} can access Orako.`
            : error
              ? 'Could not check right now. Your agent may still be connected.'
              : 'Authorize in your agent, then return to this tab.'}
        </div>
      </div>
      <button
        type="button"
        onClick={() => {
          setLoading(true)
          void load()
        }}
        style={{
          border: `1px solid ${hasConnection ? T.successBorder : T.borderStrong}`,
          background: T.surface,
          color: hasConnection ? T.successInk : T.body,
          borderRadius: 8,
          padding: '7px 10px',
          fontFamily: 'inherit',
          fontSize: 12,
          fontWeight: 600,
          cursor: 'pointer',
        }}
      >
        Check again
      </button>
    </div>
  )
}
