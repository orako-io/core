// DeleteOrganizationModal — the guarded danger-zone flow for deleting an entire
// organization from the Organization page. Mirrors DeleteProjectModal's
// typed-name confirmation, but there is no reversible "archive instead" escape:
// an org delete is a hard, irreversible cascade of every project, conversation
// and member.

import { useState } from 'react'
import { Modal } from './Modal'
import { Button } from './Button'
import { T } from '../lib/theme'

interface Props {
  open: boolean
  orgName: string
  onClose: () => void
  onConfirmDelete: () => void
  deleting?: boolean
}

export function DeleteOrganizationModal({ open, orgName, onClose, onConfirmDelete, deleting = false }: Props) {
  const [confirmText, setConfirmText] = useState('')
  const matches = confirmText.trim().toLowerCase() === orgName.trim().toLowerCase() && orgName.trim() !== ''

  function handleClose() {
    setConfirmText('')
    onClose()
  }

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title="Delete organization"
      subtitle="Permanently removes this organization — every project, conversation, member and their history. This can't be undone."
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
            Delete organization
          </Button>
        </>
      }
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div
          style={{
            padding: '12px 14px',
            border: `1px solid ${T.accentBorder}`,
            borderRadius: 11,
            background: T.accentSofter,
            fontSize: 12.5,
            color: T.subtle,
            lineHeight: 1.5,
          }}
        >
          Everyone loses access immediately and nothing here can be recovered.
        </div>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 7, marginTop: 4 }}>
          <span style={{ fontSize: 13, color: T.body }}>
            Type <strong>{orgName}</strong> to confirm
          </span>
          <input
            autoFocus
            value={confirmText}
            onChange={e => setConfirmText(e.target.value)}
            placeholder={orgName}
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
