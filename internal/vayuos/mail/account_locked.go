// SPDX-License-Identifier: Apache-2.0

package mail

import "errors"

// ErrAccountLocked is a sign-in refused because the account is locked after
// too many failures at any door (auth.CheckAccountLockout), whatever the
// password given.
var ErrAccountLocked = errors.New("vayumail: the account is locked after too many failed sign-ins; try again later")
