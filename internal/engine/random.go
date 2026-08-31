package engine

import (
	"crypto/sha256"
	"encoding/binary"
)

func streamSeed(seed, stream string) uint64 {
	b := sha256.Sum256([]byte(RNGVersion + "\x00" + seed + "\x00" + stream))
	return binary.LittleEndian.Uint64(b[:8])
}

// SplitMix64 has explicit uint64 wraparound and no dependency on Go's math/rand.
func nextRandom(state *uint64) uint64 {
	*state += 0x9e3779b97f4a7c15
	z := *state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func randomN(state *uint64, n int) int {
	if n <= 0 {
		panic("empty random pool")
	}
	bound := uint64(n)
	threshold := -bound % bound
	for {
		x := nextRandom(state)
		if x >= threshold {
			return int(x % bound)
		}
	}
}
