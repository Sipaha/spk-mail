package fsutil

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// SHA256Reader returns the lowercase hex SHA-256 of all data read from r.
func SHA256Reader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
