// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import "crypto/rand"

// slurm.key, the key of auth/slurm and cred/slurm with identity clientIds.
// sind writes it to the config volume, which every node mounts at ConfDir,
// where the daemons look for it.
const (
	SlurmKeyFile = "slurm.key"
	SlurmKeyPath = ConfDir + "/" + SlurmKeyFile
)

// SlurmKeySize is the size of slurm.key in bytes, as Slurm's documentation
// creates it (dd if=/dev/random bs=1024 count=1).
const SlurmKeySize = 1024

// GenerateSlurmKey generates a random slurm.key.
func GenerateSlurmKey() []byte {
	key := make([]byte, SlurmKeySize)
	_, _ = rand.Read(key)
	return key
}

// jwt_hs256.key, the HS256 key auth/jwt signs and checks tokens with, for
// slurmrestd on a managed api node. sind writes it to the config volume,
// where slurmctld and slurmdbd find it by AuthAltParameters' jwt_key.
const (
	JWTKeyFile = "jwt_hs256.key"
	JWTKeyPath = ConfDir + "/" + JWTKeyFile
)

// JWTKeySize is the size of jwt_hs256.key in bytes, as Slurm's JWT guide
// creates it (dd if=/dev/random bs=32 count=1).
const JWTKeySize = 32

// GenerateJWTKey generates a random jwt_hs256.key.
func GenerateJWTKey() []byte {
	key := make([]byte, JWTKeySize)
	_, _ = rand.Read(key)
	return key
}
