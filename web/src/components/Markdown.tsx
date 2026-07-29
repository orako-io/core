// Minimal, dependency-free Markdown renderer for agent-authored content
// (thread messages, resolutions). Covers what agent prose actually uses:
// paragraphs, bold, italic, inline + fenced code, bullet and numbered lists,
// small headings, links. Untrusted input → built as React nodes, never HTML.

import { T } from '../lib/theme'

const inlineCodeStyle: React.CSSProperties = {
  fontFamily: T.mono,
  fontSize: '0.92em',
  background: '#F1F3F5',
  padding: '1px 5px',
  borderRadius: 4,
}

const codeBlockStyle: React.CSSProperties = {
  fontFamily: T.mono,
  fontSize: 12.5,
  lineHeight: 1.55,
  background: '#1B1F2A',
  color: '#E4E7EC',
  padding: '12px 14px',
  borderRadius: 9,
  overflowX: 'auto',
  margin: '10px 0',
  whiteSpace: 'pre',
}

const INLINE_TOKEN = /(`[^`\n]+`|\*\*[^*\n]+\*\*|\*[^*\n]+\*|\[[^\]\n]+\]\([^)\s]+\))/g

function renderInline(text: string, keyBase: string): React.ReactNode[] {
  return text.split(INLINE_TOKEN).map((part, i) => {
    const key = `${keyBase}-${i}`
    if (part.startsWith('`') && part.endsWith('`') && part.length > 2) {
      return (
        <code key={key} style={inlineCodeStyle}>
          {part.slice(1, -1)}
        </code>
      )
    }
    if (part.startsWith('**') && part.endsWith('**') && part.length > 4) {
      return <strong key={key}>{part.slice(2, -2)}</strong>
    }
    if (part.startsWith('*') && part.endsWith('*') && part.length > 2) {
      return <em key={key}>{part.slice(1, -1)}</em>
    }
    const link = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(part)
    if (link) {
      const href = link[2]
      // Only obvious web links become anchors; anything else stays text.
      if (href.startsWith('https://') || href.startsWith('http://')) {
        return (
          <a key={key} href={href} target="_blank" rel="noreferrer" style={{ color: T.accent }}>
            {link[1]}
          </a>
        )
      }
      return <span key={key}>{part}</span>
    }
    return <span key={key}>{part}</span>
  })
}

interface Block {
  kind: 'p' | 'code' | 'ul' | 'ol' | 'h'
  lines: string[]
  lang?: string
}

function toBlocks(text: string): Block[] {
  const blocks: Block[] = []
  const lines = text.replace(/\r\n/g, '\n').split('\n')

  let i = 0
  while (i < lines.length) {
    const line = lines[i]

    if (line.trim() === '') {
      i++
      continue
    }

    if (line.startsWith('```')) {
      const lang = line.slice(3).trim()
      const body: string[] = []
      i++
      while (i < lines.length && !lines[i].startsWith('```')) {
        body.push(lines[i])
        i++
      }
      i++ // closing fence
      blocks.push({ kind: 'code', lines: body, lang })
      continue
    }

    if (/^#{1,4}\s/.test(line)) {
      blocks.push({ kind: 'h', lines: [line.replace(/^#{1,4}\s/, '')] })
      i++
      continue
    }

    if (/^\s*[-*]\s+/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*]\s+/, ''))
        i++
      }
      blocks.push({ kind: 'ul', lines: items })
      continue
    }

    if (/^\s*\d+[.)]\s+/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+[.)]\s+/, ''))
        i++
      }
      blocks.push({ kind: 'ol', lines: items })
      continue
    }

    const para: string[] = []
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !lines[i].startsWith('```') &&
      !/^\s*[-*]\s+/.test(lines[i]) &&
      !/^\s*\d+[.)]\s+/.test(lines[i]) &&
      !/^#{1,4}\s/.test(lines[i])
    ) {
      para.push(lines[i])
      i++
    }
    blocks.push({ kind: 'p', lines: para })
  }

  return blocks
}

export function Markdown({ text }: { text: string }) {
  const blocks = toBlocks(text)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {blocks.map((b, bi) => {
        switch (b.kind) {
          case 'code':
            return (
              <pre key={bi} style={codeBlockStyle}>
                {b.lines.join('\n')}
              </pre>
            )
          case 'h':
            return (
              <div key={bi} style={{ fontWeight: 700, fontSize: '1.02em', marginTop: bi ? 4 : 0 }}>
                {renderInline(b.lines[0], `h${bi}`)}
              </div>
            )
          case 'ul':
            return (
              <ul key={bi} style={{ margin: 0, paddingLeft: 20, display: 'flex', flexDirection: 'column', gap: 4 }}>
                {b.lines.map((item, ii) => (
                  <li key={ii}>{renderInline(item, `u${bi}-${ii}`)}</li>
                ))}
              </ul>
            )
          case 'ol':
            return (
              <ol key={bi} style={{ margin: 0, paddingLeft: 22, display: 'flex', flexDirection: 'column', gap: 4 }}>
                {b.lines.map((item, ii) => (
                  <li key={ii}>{renderInline(item, `o${bi}-${ii}`)}</li>
                ))}
              </ol>
            )
          default:
            return (
              <p key={bi} style={{ margin: 0, lineHeight: 1.6 }}>
                {b.lines.map((l, li) => (
                  <span key={li}>
                    {li > 0 && <br />}
                    {renderInline(l, `p${bi}-${li}`)}
                  </span>
                ))}
              </p>
            )
        }
      })}
    </div>
  )
}
