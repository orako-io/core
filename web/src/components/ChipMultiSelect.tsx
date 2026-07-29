// ChipMultiSelect — the expertise chip group used by the Invite modal.
// Renders the taxonomy (EXPERTISE_TAGS) as toggleable pill chips plus any
// custom tags already in `value`, and a "+ Custom" affordance to add a
// free-text tag. Controlled: `value: string[]` / `onChange(next)`.

import { useMemo, useState } from 'react'
import { T } from '../lib/theme'
import { MAX_TAG_COUNT, MAX_TAG_LENGTH } from '../lib/validation'
import { Icon } from './Icon'

interface Props {
  options: readonly string[]
  value: string[]
  onChange: (next: string[]) => void
  disabled?: boolean
}

// Case-insensitive membership test.
function includesTag(list: string[], tag: string): boolean {
  const t = tag.toLowerCase()
  return list.some(x => x.toLowerCase() === t)
}

export function ChipMultiSelect({ options, value, onChange, disabled = false }: Props) {
  const [adding, setAdding] = useState(false)
  const [custom, setCustom] = useState('')
  const [error, setError] = useState<string | null>(null)

  // Chips shown: the taxonomy first, then any selected custom tags not in it.
  const chips = useMemo(() => {
    const extra = value.filter(v => !includesTag([...options], v))
    return [...options, ...extra]
  }, [options, value])

  function toggle(tag: string) {
    if (disabled) return
    if (includesTag(value, tag)) {
      setError(null)
      onChange(value.filter(v => v.toLowerCase() !== tag.toLowerCase()))
    } else {
      if (value.length >= MAX_TAG_COUNT) {
        setError(`You can select up to ${MAX_TAG_COUNT} tags.`)
        return
      }
      setError(null)
      onChange([...value, tag])
    }
  }

  function commitCustom() {
    const tag = custom.trim()
    if (!tag) {
      setAdding(false)
      setCustom('')
      return
    }
    if (tag.length > MAX_TAG_LENGTH) {
      setError(`Tags can be up to ${MAX_TAG_LENGTH} characters.`)
      return
    }
    if (!includesTag(value, tag)) {
      if (value.length >= MAX_TAG_COUNT) {
        setError(`You can select up to ${MAX_TAG_COUNT} tags.`)
        return
      }
      onChange([...value, tag])
    }
    setError(null)
    setCustom('')
    setAdding(false)
  }

  return (
    <div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
      {chips.map(tag => {
        const selected = includesTag(value, tag)
        return (
          <button
            key={tag}
            type="button"
            onClick={() => toggle(tag)}
            disabled={disabled}
            aria-pressed={selected}
            title={tag}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              height: 32,
              padding: selected ? '0 11px 0 9px' : '0 12px',
              borderRadius: 999,
              fontSize: 13,
              fontWeight: 500,
              fontFamily: 'inherit',
              cursor: disabled ? 'not-allowed' : 'pointer',
              background: selected ? T.accentSoft : T.surface,
              color: selected ? T.accentInk : T.body,
              border: `1px solid ${selected ? T.accentBorder : T.borderStrong}`,
              transition: 'background .12s, border-color .12s',
              maxWidth: '100%',
              overflow: 'hidden',
            }}
          >
            {selected && <Icon name="check" size={13} color={T.accent} strokeWidth={2.4} />}
            <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{tag}</span>
          </button>
        )
      })}

      {adding ? (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <input
            autoFocus
            value={custom}
            maxLength={MAX_TAG_LENGTH}
            onChange={e => setCustom(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter') {
                e.preventDefault()
                commitCustom()
              } else if (e.key === 'Escape') {
                e.preventDefault()
                setAdding(false)
                setCustom('')
              }
            }}
            onBlur={commitCustom}
            placeholder="Custom tag"
            style={{
              height: 32,
              width: 130,
              padding: '0 10px',
              borderRadius: 999,
              border: `1px solid ${T.accentBorder}`,
              background: T.surface,
              fontSize: 13,
              fontFamily: 'inherit',
              color: T.body,
              outline: 'none',
            }}
          />
        </span>
      ) : (
        <button
          type="button"
          onClick={() => setAdding(true)}
          disabled={disabled}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 4,
            height: 32,
            padding: '0 12px 0 10px',
            borderRadius: 999,
            fontSize: 13,
            fontWeight: 500,
            fontFamily: 'inherit',
            cursor: disabled ? 'not-allowed' : 'pointer',
            background: T.surface,
            color: T.accent,
            border: `1px dashed ${T.accentBorder}`,
          }}
        >
          <Icon name="plus" size={13} color={T.accent} strokeWidth={2.2} />
          Custom
        </button>
      )}
      </div>
      {adding && (
        <div style={{ marginTop: 6, fontSize: 11.5, color: T.faint }}>
          {custom.length}/{MAX_TAG_LENGTH}
        </div>
      )}
      {error && <div role="alert" style={{ marginTop: 7, fontSize: 12.5, color: T.dangerInk }}>{error}</div>}
    </div>
  )
}
