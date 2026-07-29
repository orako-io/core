// SPDX-License-Identifier: Apache-2.0

package auth

import "golang.org/x/crypto/bcrypt"

// maxPasswordBytes is bcrypt's hard input limit. bcrypt silently ignores bytes
// past 72, so we reject longer inputs rather than hash a truncated password.
const maxPasswordBytes = 72

// HashPassword returns a bcrypt hash of the plaintext password. It errors on an
// over-length password (bcrypt would otherwise truncate at 72 bytes). Callers
// should also enforce a minimum length before hashing.
func HashPassword(plain string) (string, error) {
	if len(plain) > maxPasswordBytes {
		return "", bcrypt.ErrPasswordTooLong
	}

	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// dummyHash is a valid bcrypt hash used to spend one bcrypt comparison's worth of
// time when the account does not exist, so an unauthenticated caller cannot tell
// "account exists" from "does not" by response latency (username enumeration).
// Precomputed once at init so the equalizing compare is the same cost every call.
//
//nolint:gochecknoglobals // immutable, precomputed timing equalizer
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("orako-login-timing-equalizer"), bcrypt.DefaultCost)

// VerifyPasswordConstantTime is like VerifyPassword but ALWAYS performs a bcrypt
// comparison: for an empty hash (unknown / IdP-only account) it compares against
// a dummy hash and returns false, so both the account-exists and
// account-missing paths take the same time.
func VerifyPasswordConstantTime(hash, plain string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain))

		return false
	}

	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
