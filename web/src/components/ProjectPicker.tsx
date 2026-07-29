// Project picker — active-project selector bound to the identity context.
//
// Multi-project accounts switch scope here; the selection drives every
// project-scoped page (via useIdentity().selectedProjectId). Hidden when the
// account has a single project (nothing to pick) or none (onboarding).

import { useIdentity } from '../lib/identity'
import { T } from '../lib/theme'
import { Icon } from './Icon'

export function ProjectPicker() {
  const { projects, selectedProjectId, setSelectedProjectId } = useIdentity()

  if (projects.length <= 1) return null

  return (
    <label style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
      <Icon name="folder" size={15} color={T.subtle} />
      <select
        value={selectedProjectId}
        onChange={e => setSelectedProjectId(e.target.value)}
        aria-label="Active project"
        style={{
          background: T.surface,
          border: `1px solid ${T.borderStrong}`,
          borderRadius: T.rSm,
          color: T.body,
          padding: '6px 10px',
          fontSize: 13,
          fontFamily: 'inherit',
          maxWidth: 220,
        }}
      >
        {projects.map(p => (
          <option key={p.id} value={p.id}>
            {p.name || p.id}
          </option>
        ))}
      </select>
    </label>
  )
}
