// DeleteProjectModal — the guarded danger-zone delete flow shared by the
// Projects list row menu and the Project detail page: nudges toward Archive
// (reversible) instead, and requires typing the project's name before Delete
// becomes clickable.

import { useState } from 'react'
import { Modal } from './Modal'
import { Button } from './Button'
import { T } from '../lib/theme'

interface Props {
  open: boolean
  projectName: string
  onClose: () => void
  onArchiveInstead: () => void
  onConfirmDelete: () => void
  archiving?: boolean
  deleting?: boolean
}

export function DeleteProjectModal({
  open,
  projectName,
  onClose,
  onArchiveInstead,
  onConfirmDelete,
  archiving = false,
  deleting = false,
}: Props) {
  const [confirmText, setConfirmText] = useState('')
  const matches = confirmText.trim() === projectName && projectName.trim() !== ''

  function handleClose() {
    setConfirmText('')
    onClose()
  }

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title="Delete project"
      subtitle="Permanently removes this project, its conversations and their history. This can't be undone."
      width={520}
      footer={
        <>
          <Button variant="secondary" onClick={handleClose} disabled={deleting}>
            Cancel
          </Button>
          <Button
            variant="primary"
            style={{ background: '#DC2626' }}
            disabled={!matches || deleting}
            loading={deleting}
            onClick={onConfirmDelete}
          >
            Delete project
          </Button>
        </>
      }
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 14,
            padding: '12px 14px',
            border: `1px solid ${T.accentBorder}`,
            borderRadius: 11,
            background: T.accentSofter,
          }}
        >
          <div>
            <div style={{ fontSize: 13.5, fontWeight: 600, color: T.text }}>Archive instead?</div>
            <div style={{ fontSize: 12, color: T.subtle, marginTop: 1 }}>
              Reversible — stops routing, keeps everything, reactivate anytime.
            </div>
          </div>
          <Button variant="secondary" size="sm" loading={archiving} onClick={onArchiveInstead} style={{ flex: 'none' }}>
            Archive
          </Button>
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 7, marginTop: 4 }}>
          <span style={{ fontSize: 13, color: T.body }}>
            Type <strong>{projectName}</strong> to confirm
          </span>
          <input
            autoFocus
            value={confirmText}
            onChange={e => setConfirmText(e.target.value)}
            placeholder={projectName}
            disabled={deleting}
            style={{
              height: 42,
              border: `1px solid ${T.borderStrong}`,
              borderRadius: 10,
              padding: '0 13px',
              fontSize: 14,
              color: T.body,
              background: T.surface,
              fontFamily: 'inherit',
            }}
          />
        </label>
      </div>
    </Modal>
  )
}
