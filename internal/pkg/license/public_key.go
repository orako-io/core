// SPDX-License-Identifier: AGPL-3.0-or-later

package license

// DefaultPublicKeyB64 is Orako's official Ed25519 license-signing public key,
// base64-encoded. It is baked into the binary so a paid self-host only has to
// paste its license key in the dashboard — the public key it verifies against
// ships with the product (a public key is not a secret; the private signing half
// lives only in the hosted license forge).
const DefaultPublicKeyB64 = "1nCylJ68V9w+abgWIkPAFnjf0GOPCw2mKwUv8zzzd6g="

// DefaultRefreshURL is Orako's hosted license-refresh endpoint, baked in so a paid
// self-host renews transparently with no configuration: the background edition
// loop POSTs the instance's current key here to pull a freshly-minted one on
// renewal. Only a LICENSED instance ever calls it — a Community (keyless) instance
// never phones home. Setting ORAKO_LICENSE_OFFLINE=true disables it entirely (pure
// offline, air-gapped); ORAKO_LICENSE_REFRESH_URL can point to another endpoint.
const DefaultRefreshURL = "https://app.orako.io/billing/license/refresh"
