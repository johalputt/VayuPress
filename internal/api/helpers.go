package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x%s", time.Now().UnixNano(), strings.Repeat("0", 16))
	}
	return hex.EncodeToString(b)
}
