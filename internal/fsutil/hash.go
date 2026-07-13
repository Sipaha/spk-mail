package fsutil

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// sha256Copy streams src into dst while computing SHA-256. Returns the
// lowercase-hex digest and the number of bytes copied.
func sha256Copy(dst io.Writer, src io.Reader) (string, int64, error) {
	h := sha256.New()
	tee := io.TeeReader(src, h)
	n, err := io.Copy(dst, tee)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// SHA256Reader returns the lowercase hex SHA-256 of all data read from r.
func SHA256Reader(r io.Reader) (string, error) {
	sha, _, err := sha256Copy(io.Discard, r)
	return sha, err
}
