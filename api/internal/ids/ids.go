package ids

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// New returns a prefixed, URL-safe random ID like "usr_jbsxk3lfmrdwc".
// 10 bytes of entropy → 16 lowercase base32 characters (no padding).
func New(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("ids: crypto/rand failed: " + err.Error())
	}
	s := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
	return prefix + "_" + s
}

func User() string        { return New("usr") }
func Token() string       { return New("tok") }
func Workspace() string   { return New("ws") }
func Environment() string { return New("env") }
func File() string        { return New("file") }
func AuditLog() string    { return New("log") }
