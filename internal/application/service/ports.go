// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"io"
	"time"

	"github.com/orako-io/core/internal/application/domain/model"
)

// This file gathers the application-layer service ports — the capabilities the
// use cases call out through, distinct from the aggregate repositories in
// domain/repository. The query and event packages import the ones they need.
// Concrete implementations live under internal/adapters.

// Mailer sends transactional email (invitations, billing alerts, the
// dashboard-channel notification). One adapter serves both deployment modes by
// configuration; a no-op mailer is used when email is unconfigured.
type Mailer interface {
	Send(ctx context.Context, msg model.EmailMessage) error
}

// BlobStore stores attachment bytes. The concrete adapter is S3-compatible; a
// no-op returns adapters/blobstore.ErrDisabled from every method when object
// storage is unconfigured. Enabled reports whether real storage is wired.
type BlobStore interface {
	Put(ctx context.Context, key string, r io.Reader, contentType string, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	SignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	Delete(ctx context.Context, key string) error
	Enabled() bool
}

// InviteLinkGenerator mints the signed action token embedded in invitation
// emails. linkType is "invite" for a new account or "magiclink" for an existing
// one (self-host always mints "invite").
type InviteLinkGenerator interface {
	GenerateInviteLink(ctx context.Context, email string) (hashedToken, linkType string, err error)
}

// Transactor runs a unit of work across several repositories in one transaction,
// propagated through the context handed to fn, so a use case composes an atomic
// multi-aggregate operation without importing any database driver.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}
