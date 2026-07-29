// SPDX-License-Identifier: AGPL-3.0-or-later

// Package edition resolves which product edition the server runs as, and the
// resource limits that apply. It maps three mutually exclusive triggers to a
// cached Limits value at boot:
//
//   - SaaS      — the private managed-service overlay governs limits.
//   - Licensed  — a valid offline license key verifies. Limits come from the
//     signed token.
//   - Community — neither. The free self-host tier: hard caps of 5 members,
//     1 org, 1 project.
//
// The distinction keeps community caps off managed deployments.
package edition

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"slices"

	"github.com/orako-io/core/internal/pkg/license"
)

// Kind is the resolved edition.
type Kind int

const (
	// Community is the free self-host tier with hard resource caps.
	Community Kind = iota
	// Licensed is a self-host tier unlocked by a valid license key.
	Licensed
	// SaaS is the managed hosted deployment.
	SaaS
)

// String returns a lower-case label for logs.
func (k Kind) String() string {
	switch k {
	case Community:
		return "community"
	case Licensed:
		return "licensed"
	case SaaS:
		return "saas"
	default:
		return "unknown"
	}
}

// Limits is a resolved set of resource ceilings. A zero value for any field
// means unlimited — so the SaaS and unlimited-license editions use zeros, and
// only Community carries concrete non-zero caps.
type Limits struct {
	// MaxMembers is the ceiling on members per organisation (0 = unlimited).
	MaxMembers int
	// MaxOrgs is the ceiling on organisations in the instance (0 = unlimited).
	MaxOrgs int
	// MaxProjects is the ceiling on projects per organisation (0 = unlimited).
	MaxProjects int
}

// CommunityLimits are the free self-host caps: 5 members, 1 org, 1 project.
var CommunityLimits = Limits{MaxMembers: 5, MaxOrgs: 1, MaxProjects: 1} //nolint:gochecknoglobals // compile-time constant caps; package-level by design

// Edition is the resolved edition plus its limits and feature flags.
type Edition struct {
	Kind     Kind
	Limits   Limits
	Features []string
}

// Allows reports whether the edition grants a named premium feature. SaaS
// grants everything; other editions grant only what their license lists.
func (e Edition) Allows(feature string) bool {
	if e.Kind == SaaS {
		return true
	}

	return slices.Contains(e.Features, feature)
}

// Enforced reports whether resource caps apply to this edition at the write path.
// Community (the free 5/1/1 caps) and Licensed (the paid limits stated in the
// token — seats/orgs/projects) both enforce; SaaS does not because the private
// overlay governs limits. A 0 limit always means unlimited, so a
// Licensed token that omits a limit is uncapped on that axis.
func (e Edition) Enforced() bool {
	return e.Kind != SaaS
}

// Resolve determines the edition at boot.
//
// managed identifies the private hosted build. When true the edition is SaaS
// and no community caps apply, regardless of any license key. Otherwise, when
// both a license key and public key are present,
// the key is verified offline; a valid key yields a Licensed edition with the
// token's limits. A malformed, unverifiable, or expired key does NOT unlock
// anything: Resolve returns the Community edition together with a non-nil error
// so the caller can log a warning. Absent keys yield Community with a nil error.
func Resolve(managed bool, licenseKeyB64, publicKeyB64 string) (Edition, error) {
	if managed {
		return Edition{Kind: SaaS, Limits: Limits{MaxMembers: 0, MaxOrgs: 0, MaxProjects: 0}, Features: nil}, nil
	}

	community := Edition{Kind: Community, Limits: CommunityLimits, Features: nil}

	if licenseKeyB64 == "" || publicKeyB64 == "" {
		return community, nil
	}

	pub, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return community, fmt.Errorf("edition: decoding license public key: %w", err)
	}

	if len(pub) != ed25519.PublicKeySize {
		return community, fmt.Errorf("edition: license public key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}

	envelope, err := base64.StdEncoding.DecodeString(licenseKeyB64)
	if err != nil {
		return community, fmt.Errorf("edition: decoding license key: %w", err)
	}

	lic, err := license.Verify(envelope, ed25519.PublicKey(pub))
	if err != nil {
		return community, fmt.Errorf("edition: verifying license, falling back to community: %w", err)
	}

	return Edition{
		Kind: Licensed,
		Limits: Limits{
			MaxMembers:  lic.Seats,
			MaxOrgs:     lic.MaxOrgs,
			MaxProjects: lic.MaxProjects,
		},
		Features: lic.Features,
	}, nil
}
