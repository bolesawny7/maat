package plan

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// newUUIDv7 returns a time-ordered UUID (RFC 9562, version 7): a 48-bit
// millisecond timestamp followed by random bits, with the version and variant
// nibbles set. Time ordering means plans sort by creation time by their ID
// alone, which is convenient for listing. maat implements it directly to avoid a
// dependency for a few lines of code.
func newUUIDv7() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic and not expected; fall back to a
		// timestamp-only ID rather than panicking in a library.
		ms := uint64(time.Now().UnixMilli())
		for i := 5; i >= 0; i-- {
			b[i] = byte(ms)
			ms >>= 8
		}
	} else {
		ms := uint64(time.Now().UnixMilli())
		b[0] = byte(ms >> 40)
		b[1] = byte(ms >> 32)
		b[2] = byte(ms >> 24)
		b[3] = byte(ms >> 16)
		b[4] = byte(ms >> 8)
		b[5] = byte(ms)
	}
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return format(b)
}

func format(b [16]byte) string {
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
