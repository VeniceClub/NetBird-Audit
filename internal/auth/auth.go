package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time
	ttl      time.Duration
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{sessions: map[string]time.Time{}, ttl: ttl}
}

func (s *SessionStore) New() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := fmt.Sprintf("%x", b)
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(s.ttl)
	s.mu.Unlock()
	return token, nil
}

func (s *SessionStore) Valid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[token]
	if !ok || time.Now().After(exp) {
		delete(s.sessions, token)
		return false
	}
	s.sessions[token] = time.Now().Add(s.ttl)
	return true
}

func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(b), err
}

func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

func ValidateTOTP(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	for offset := -1; offset <= 1; offset++ {
		if totpCode(secret, now.Add(time.Duration(offset)*30*time.Second)) == code {
			return true
		}
	}
	return false
}

func totpCode(secret string, t time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return ""
	}
	counter := uint64(t.Unix() / 30)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	binCode := (uint32(sum[off])&0x7f)<<24 | (uint32(sum[off+1])&0xff)<<16 | (uint32(sum[off+2])&0xff)<<8 | uint32(sum[off+3])&0xff
	otp := binCode % uint32(math.Pow10(6))
	return fmt.Sprintf("%06d", otp)
}

func OTPAuthURI(secret, account, issuer string) (string, error) {
	if secret == "" || account == "" {
		return "", errors.New("secret and account are required")
	}
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&digits=6&period=30", issuer, account, secret, issuer), nil
}
