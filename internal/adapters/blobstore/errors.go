// SPDX-License-Identifier: AGPL-3.0-or-later

package blobstore

import "errors"

// ErrDisabled is returned by the no-op blob store when object storage is not
// configured (self-host without S3): attachment features degrade off with a
// clear signal rather than a nil-pointer panic.
var ErrDisabled = errors.New("blob storage is not configured (set the ORAKO_S3_* env vars to enable attachments)")
