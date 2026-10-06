package jwt

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
)

type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type BotClaims struct {
	BotID     string  `json:"bot_id"`
	OwnerType string  `json:"owner_type"`
	OwnerID   *string `json:"owner_id,omitempty"`
	Type      string  `json:"type"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret     []byte
	expireSec  int
	refreshSec int
}

func NewManager(secret string, expireSec, refreshSec int) *Manager {
	if expireSec <= 0 {
		expireSec = 7200
	}
	if refreshSec <= 0 {
		refreshSec = 604800
	}
	return &Manager{
		secret:     []byte(secret),
		expireSec:  expireSec,
		refreshSec: refreshSec,
	}
}

func (m *Manager) ExpireSeconds() int { return m.expireSec }

func (m *Manager) Generate(userID, username string) (string, error) {
	if err := identity.Validate(userID); err != nil {
		return "", errors.Wrap(errors.CodeUnauthorized, "invalid user identity", err)
	}
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "aim",
			Subject:   userID,
			ID:        uuid.New().String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(m.expireSec) * time.Second)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(m.secret)
	if err != nil {
		return "", errors.Wrap(errors.CodeUnauthorized, "token sign failed", err)
	}
	return tokenStr, nil
}

func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New(errors.CodeUnauthorized, "unexpected signing method")
		}
		return m.secret, nil
	}, jwt.WithLeeway(time.Duration(0)))
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "token parse failed", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New(errors.CodeUnauthorized, "invalid token")
	}
	if err := identity.Validate(claims.UserID); err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "invalid user identity", err)
	}
	return claims, nil
}

// ParseIgnoreExpiry parses a token without validating expiration.
// Used for logout/revocation where we only need the jti (claims.ID).
func (m *Manager) ParseIgnoreExpiry(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New(errors.CodeUnauthorized, "unexpected signing method")
		}
		return m.secret, nil
	}, jwt.WithoutClaimsValidation())
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "token parse failed", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New(errors.CodeUnauthorized, "invalid token")
	}
	if err := identity.Validate(claims.UserID); err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "invalid user identity", err)
	}
	return claims, nil
}

func (m *Manager) Refresh(tokenStr string) (string, error) {
	claims, err := m.Parse(tokenStr)
	if err != nil {
		return "", err
	}
	if claims == nil {
		return "", errors.New(errors.CodeUnauthorized, "invalid token for refresh")
	}
	return m.Generate(claims.UserID, claims.Username)
}

func (m *Manager) GenerateBotToken(botID, ownerType string, ownerID *string, botType string) (string, error) {
	if err := validateBotIdentity(botID, ownerType, ownerID); err != nil {
		return "", err
	}
	now := time.Now()
	claims := BotClaims{
		BotID:     botID,
		OwnerType: ownerType,
		OwnerID:   ownerID,
		Type:      botType,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "aim:bot",
			Subject:   botID,
			ID:        uuid.New().String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(m.expireSec) * time.Second)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(m.secret)
	if err != nil {
		return "", errors.Wrap(errors.CodeUnauthorized, "bot token sign failed", err)
	}
	return tokenStr, nil
}

func (m *Manager) ParseBotToken(tokenStr string) (*BotClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &BotClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New(errors.CodeUnauthorized, "unexpected signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeUnauthorized, "bot token parse failed", err)
	}
	claims, ok := token.Claims.(*BotClaims)
	if !ok || !token.Valid {
		return nil, errors.New(errors.CodeUnauthorized, "invalid bot token")
	}
	if err := validateBotIdentity(claims.BotID, claims.OwnerType, claims.OwnerID); err != nil {
		return nil, err
	}
	return claims, nil
}

func validateBotIdentity(botID, ownerType string, ownerID *string) error {
	if err := identity.Validate(botID); err != nil {
		return errors.Wrap(errors.CodeUnauthorized, "invalid bot identity", err)
	}
	switch ownerType {
	case "platform":
		if ownerID != nil {
			return errors.New(errors.CodeUnauthorized, "platform bot cannot have user owner")
		}
	case "user":
		if ownerID == nil {
			return errors.New(errors.CodeUnauthorized, "missing bot owner")
		}
		if err := identity.Validate(*ownerID); err != nil {
			return errors.Wrap(errors.CodeUnauthorized, "invalid bot owner", err)
		}
	default:
		return errors.New(errors.CodeUnauthorized, "invalid bot ownership")
	}
	return nil
}
