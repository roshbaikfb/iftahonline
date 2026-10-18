package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/roshbaikfb/iftahonline/internal/modules/identity/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// Challenge purposes. A code issued for one purpose never works for another.
const (
	purposeLogin       = "login"        // phone OTP sign-in
	purposeVerifyPhone = "verify_phone" // proving a new phone number
	purposeMFA         = "mfa"          // second factor after password or OTP
)

// MFATTL bounds the time between the first factor and the second.
const MFATTL = 5 * time.Minute

// Challenge is a pending verification the client completes with a code.
type Challenge struct {
	ID        string
	ExpiresAt time.Time
}

// errBadCode is the single answer for wrong, expired, used-up or unknown
// challenges, so none can be told apart.
var errBadCode = errs.Unauthenticated("invalid or expired code")

type challengeData struct {
	Purpose string  `json:"p"`
	UserID  ulid.ID `json:"u"`
	Phone   string  `json:"ph,omitempty"` // E.164, for phone verification
	MAC     []byte  `json:"m,omitempty"`  // HMAC of the code; empty for MFA challenges
}

func challengeKey(id string) string { return "identity:challenge:" + id }
func triesKey(id string) string     { return "identity:challenge:" + id + ":tries" }
func deliveryKey(id string) string  { return "identity:delivery:" + id }

// putChallenge stores d sealed (it may hold a phone number) under id.
func (s *Service) putChallenge(ctx context.Context, id string, d challengeData, ttl time.Duration) (Challenge, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return Challenge{}, fmt.Errorf("identity: challenge: %w", err)
	}
	sealed, err := s.keyring.Seal(raw, []byte("challenge:"+id))
	if err != nil {
		return Challenge{}, err
	}
	if err := s.rdb.Set(ctx, challengeKey(id), sealed, ttl).Err(); err != nil {
		return Challenge{}, errs.Unavailable(5*time.Second, fmt.Errorf("identity: store challenge: %w", err))
	}
	return Challenge{ID: id, ExpiresAt: s.now().Add(ttl)}, nil
}

// attempt counts one try against challenge id and returns its data. After
// OTPMaxAttempts tries the challenge is destroyed. The counter is an
// atomic INCR, so concurrent guesses cannot exceed the budget.
func (s *Service) attempt(ctx context.Context, id, purpose string) (challengeData, error) {
	if _, err := ulid.Parse(id); err != nil {
		return challengeData{}, errBadCode
	}
	pipe := s.rdb.TxPipeline()
	incr := pipe.Incr(ctx, triesKey(id))
	pipe.Expire(ctx, triesKey(id), domain.OTPTTL+time.Minute)
	get := pipe.Get(ctx, challengeKey(id))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return challengeData{}, errs.Unavailable(5*time.Second, fmt.Errorf("identity: challenge: %w", err))
	}
	sealed, err := get.Bytes()
	if err != nil {
		return challengeData{}, errBadCode
	}
	if incr.Val() > domain.OTPMaxAttempts {
		s.dropChallenge(ctx, id)
		return challengeData{}, errBadCode
	}
	raw, err := s.keyring.Open(sealed, []byte("challenge:"+id))
	if err != nil {
		return challengeData{}, errBadCode
	}
	var d challengeData
	if err := json.Unmarshal(raw, &d); err != nil || d.Purpose != purpose {
		return challengeData{}, errBadCode
	}
	return d, nil
}

func (s *Service) dropChallenge(ctx context.Context, id string) {
	if err := s.rdb.Del(ctx, challengeKey(id), triesKey(id), deliveryKey(id)).Err(); err != nil {
		s.log.WarnContext(ctx, "identity: drop challenge", "err", err)
	}
}

// checkCode compares a submitted code with the challenge's MAC.
func (s *Service) checkCode(id string, d challengeData, code string) bool {
	return len(d.MAC) > 0 && hmac.Equal(d.MAC, domain.OTPMAC(s.pepper, id, d.Purpose, code))
}

// delivery is what the send job needs; sealed in Redis, never in the job
// table.
type delivery struct {
	Phone string `json:"ph,omitempty"`
	Text  string `json:"t"`
}

func (s *Service) putDelivery(ctx context.Context, key string, aad string, d delivery, ttl time.Duration) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("identity: delivery: %w", err)
	}
	sealed, err := s.keyring.Seal(raw, []byte(aad))
	if err != nil {
		return err
	}
	if err := s.rdb.Set(ctx, key, sealed, ttl).Err(); err != nil {
		return errs.Unavailable(5*time.Second, fmt.Errorf("identity: store delivery: %w", err))
	}
	return nil
}

// limitKey hides phone numbers in Redis key names.
func (s *Service) limitKey(prefix, value string) string {
	m := hmac.New(sha256.New, s.pepper)
	m.Write([]byte(prefix + "\x00" + value))
	return prefix + ":" + hex.EncodeToString(m.Sum(nil)[:16])
}
