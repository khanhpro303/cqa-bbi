package pkg

import (
	"crypto/rand"
	"math/big"
)

const verifyTokenCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// NewVerifyToken returns a 6-character pairing code ("verify <code>") drawn
// from crypto/rand. The old generator seeded math/rand with the clock, so a
// code could be guessed from the time it was issued.
func NewVerifyToken() string {
	b := make([]byte, 6)
	max := big.NewInt(int64(len(verifyTokenCharset)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("crypto/rand unavailable: " + err.Error())
		}
		b[i] = verifyTokenCharset[n.Int64()]
	}
	return string(b)
}
