// DeleteConversationModal — the guarded hard-delete confirm for a conversation,
// shown only to org admins. Lighter than DeleteProjectModal (no export/archive,
// no type-to-confirm): a conversation has no memorable name, and this is
// cleanup of spam / tests / mistakes, not a tenant-level destruction.

import { Modal } from './Modal'
import { Button } from './Button'
import { T } from '../lib/theme'

interface Props {
  open: boolean
  onClose: () => void
  onConfirmDelete: () => void
  deleting?: boolean
}

export function DeleteConversationModal({ open, onClose, onConfirmDelete, deleting = false }: Props) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Delete conversation"
      subtitle="Permanently removes this conversation and its messages. This can't be undone."
      width={460}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={deleting}>
            Cancel
          </Button>
          <Button
            variant="primary"
            style={{ background: '#DC2626' }}
            loading={deleting}
            disabled={deleting}
            onClick={onConfirmDelete}
          >
            Delete conversation
          </Button>
        </>
      }
    >
      <div style={{ fontSize: 13.5, color: T.body }}>
        The conversation and everything derived from it are deleted. Use this to clean up spam, tests, or mistakes.
      </div>
    </Modal>
  )
}
