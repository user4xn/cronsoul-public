package store

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/argon2"
	"strings"
)

func argonHash(p string) (string, error) {
	s := make([]byte, 16)
	if _, e := rand.Read(s); e != nil {
		return "", e
	}
	h := argon2.IDKey([]byte(p), s, 3, 64*1024, 4, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(s), base64.RawStdEncoding.EncodeToString(h)), nil
}
func argonVerify(encoded, p string) bool {
	a := strings.Split(encoded, "$")
	if len(a) != 6 {
		return false
	}
	var salt, expect []byte
	var e error
	if salt, e = base64.RawStdEncoding.DecodeString(a[4]); e != nil {
		return false
	}
	if expect, e = base64.RawStdEncoding.DecodeString(a[5]); e != nil {
		return false
	}
	got := argon2.IDKey([]byte(p), salt, 3, 64*1024, 4, uint32(len(expect)))
	if len(got) != len(expect) {
		return false
	}
	var x byte
	for i := range got {
		x |= got[i] ^ expect[i]
	}
	return x == 0
}
