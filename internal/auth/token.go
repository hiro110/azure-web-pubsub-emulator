package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	DefaultTokenExpiry = 60 * time.Minute
	MaxTokenExpiry     = 24 * 60 * time.Minute
)

// TokenClaims holds decoded claims from a client access token.
type TokenClaims struct {
	UserID   string
	Roles    []string
	Groups   []string
	Audience []string // raw "aud" claim values
}

// IsValidForHub returns true if at least one audience value ends with
// "/client/hubs/{hubName}". The check is suffix-based to tolerate differences
// in scheme (http vs ws) and host:port between token issuance and connection.
func (c *TokenClaims) IsValidForHub(hubName string) bool {
	suffix := "/client/hubs/" + hubName
	for _, a := range c.Audience {
		if len(a) >= len(suffix) && a[len(a)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}

// GenerateClientToken generates a JWT client access token signed with HS256.
// audience is the hub endpoint URL, e.g. "http://localhost:8080/client/hubs/hub1".
func GenerateClientToken(accessKey []byte, audience, userID string, roles, groups []string, expiry time.Duration) (string, error) {
	if expiry <= 0 {
		expiry = DefaultTokenExpiry
	}
	if expiry > MaxTokenExpiry {
		expiry = MaxTokenExpiry
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(expiry).Unix(),
	}
	if userID != "" {
		claims["sub"] = userID
	}
	if len(roles) > 0 {
		claims["role"] = roles
	}
	if len(groups) > 0 {
		claims["webpubsub.group"] = groups
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(accessKey)
}

// ValidateClientToken parses and validates a JWT client access token.
func ValidateClientToken(tokenStr string, accessKey []byte) (*TokenClaims, error) {
	token, err := jwt.Parse(tokenStr,
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return accessKey, nil
		},
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	claims := &TokenClaims{}

	if aud, err := mapClaims.GetAudience(); err == nil {
		claims.Audience = aud
	}
	if sub, ok := mapClaims["sub"].(string); ok {
		claims.UserID = sub
	}
	if roles, ok := mapClaims["role"].([]interface{}); ok {
		for _, r := range roles {
			if s, ok := r.(string); ok {
				claims.Roles = append(claims.Roles, s)
			}
		}
	}
	if groups, ok := mapClaims["webpubsub.group"].([]interface{}); ok {
		for _, g := range groups {
			if s, ok := g.(string); ok {
				claims.Groups = append(claims.Groups, s)
			}
		}
	}

	return claims, nil
}
