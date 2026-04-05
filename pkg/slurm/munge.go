// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import "crypto/rand"

// Container paths for munge configuration.
const (
	MungeDir     = "/etc/munge"
	MungeKeyFile = "munge.key"
	MungeKeyPath = MungeDir + "/" + MungeKeyFile
)

// MungeKeySize is the default munge key size in bytes (1024 bits),
// matching the mungekey --create default.
const MungeKeySize = 128

// GenerateMungeKey generates a random munge authentication key.
func GenerateMungeKey() []byte {
	key := make([]byte, MungeKeySize)
	_, _ = rand.Read(key)
	return key
}
