// SPDX-License-Identifier: LGPL-3.0-or-later

// Package prereq checks, in the integration tests, that the host meets
// sind's prerequisites. It is a package of its own, and not part of
// internal/testutil, because it imports internal/doctor, whose tests import
// internal/testutil.
package prereq
