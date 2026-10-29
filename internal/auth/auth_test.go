package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("unexpected format %s", h)
	}
	if ok, _ := VerifyPassword(h, "correct horse battery staple"); !ok {
		t.Fatal("valid password rejected")
	}
	if ok, _ := VerifyPassword(h, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := VerifyPassword("$2a$12$bcrypt", "x"); err == nil {
		t.Fatal("foreign hash format should error")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("salt not random")
	}
}

func TestRateLimiter(t *testing.T) {
	l := newRateLimiter(2, 50*time.Millisecond)
	if !l.allow("a") || !l.allow("a") || l.allow("a") {
		t.Fatal("limit not enforced")
	}
	if !l.allow("b") {
		t.Fatal("limits must be per key")
	}
	time.Sleep(60 * time.Millisecond)
	if !l.allow("a") {
		t.Fatal("window should expire")
	}
}

func TestSignedCookie(t *testing.T) {
	m := &Manager{secret: []byte("0123456789abcdef0123456789abcdef")}
	v := "token"
	c := v + "." + m.sign(v)
	if got, ok := m.verifySigned(c); !ok || got != v {
		t.Fatal("valid signature rejected")
	}
	if _, ok := m.verifySigned("token.forged"); ok {
		t.Fatal("forged signature accepted")
	}
}
