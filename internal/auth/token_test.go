package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/auth"
)

const testAudience = "http://localhost:8080/client/hubs/hub1"

func TestGenerateClientToken_Valid(t *testing.T) {
	tokenStr, err := auth.GenerateClientToken(testAccessKey, testAudience, "user1", nil, nil, 0)
	if err != nil {
		t.Fatalf("GenerateClientToken: %v", err)
	}
	if tokenStr == "" {
		t.Error("expected non-empty token")
	}
	// Must be 3-part JWT
	if len(strings.Split(tokenStr, ".")) != 3 {
		t.Errorf("expected JWT format, got: %q", tokenStr)
	}
}

func TestGenerateAndValidate_RoundTrip(t *testing.T) {
	tokenStr, err := auth.GenerateClientToken(
		testAccessKey,
		testAudience,
		"user1",
		[]string{"webpubsub.joinLeaveGroup", "webpubsub.sendToGroup"},
		[]string{"group1", "group2"},
		30*time.Minute,
	)
	if err != nil {
		t.Fatalf("GenerateClientToken: %v", err)
	}

	claims, err := auth.ValidateClientToken(tokenStr, testAccessKey)
	if err != nil {
		t.Fatalf("ValidateClientToken: %v", err)
	}

	if claims.UserID != "user1" {
		t.Errorf("UserID: got %q, want user1", claims.UserID)
	}
	if len(claims.Roles) != 2 {
		t.Errorf("Roles: got %v, want 2 items", claims.Roles)
	}
	if len(claims.Groups) != 2 {
		t.Errorf("Groups: got %v, want 2 items", claims.Groups)
	}
}

func TestGenerateClientToken_AnonymousUser(t *testing.T) {
	tokenStr, err := auth.GenerateClientToken(testAccessKey, testAudience, "", nil, nil, 0)
	if err != nil {
		t.Fatalf("GenerateClientToken: %v", err)
	}

	claims, err := auth.ValidateClientToken(tokenStr, testAccessKey)
	if err != nil {
		t.Fatalf("ValidateClientToken: %v", err)
	}

	if claims.UserID != "" {
		t.Errorf("expected empty userId for anonymous, got %q", claims.UserID)
	}
}

func TestGenerateClientToken_ExpiryDefaults(t *testing.T) {
	// Zero expiry should use default (1 hour)
	tokenStr, err := auth.GenerateClientToken(testAccessKey, testAudience, "", nil, nil, 0)
	if err != nil {
		t.Fatalf("GenerateClientToken: %v", err)
	}

	// Parse without validation to check exp claim directly
	token, _, err := jwt.NewParser().ParseUnverified(tokenStr, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	mapClaims, _ := token.Claims.(jwt.MapClaims)
	exp, _ := mapClaims["exp"].(float64)
	iat, _ := mapClaims["iat"].(float64)

	diff := exp - iat
	if diff < 3590 || diff > 3610 {
		t.Errorf("expected ~3600s expiry, got %.0fs", diff)
	}
}

func TestGenerateClientToken_ExpiryCapAtMax(t *testing.T) {
	tokenStr, err := auth.GenerateClientToken(testAccessKey, testAudience, "", nil, nil, 100*24*time.Hour)
	if err != nil {
		t.Fatalf("GenerateClientToken: %v", err)
	}

	token, _, err := jwt.NewParser().ParseUnverified(tokenStr, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	mapClaims, _ := token.Claims.(jwt.MapClaims)
	exp, _ := mapClaims["exp"].(float64)
	iat, _ := mapClaims["iat"].(float64)
	diff := exp - iat

	maxSeconds := float64(auth.MaxTokenExpiry / time.Second)
	if diff > maxSeconds+10 {
		t.Errorf("expiry should be capped at %vs, got %.0fs", maxSeconds, diff)
	}
}

func TestValidateClientToken_ExpiredToken(t *testing.T) {
	// Generate token and manually backdate exp claim by forging with a past exp
	claims := jwt.MapClaims{
		"aud": testAudience,
		"iat": float64(time.Now().Add(-2 * time.Hour).Unix()),
		"exp": float64(time.Now().Add(-1 * time.Hour).Unix()),
		"sub": "user1",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString(testAccessKey)

	_, err := auth.ValidateClientToken(tokenStr, testAccessKey)
	if err == nil {
		t.Error("expected error for expired token, got nil")
	}
}

func TestValidateClientToken_WrongKey(t *testing.T) {
	tokenStr, _ := auth.GenerateClientToken(testAccessKey, testAudience, "user1", nil, nil, 0)
	wrongKey := mustDecodeBase64("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=")

	_, err := auth.ValidateClientToken(tokenStr, wrongKey)
	if err == nil {
		t.Error("expected error for wrong key, got nil")
	}
}

func TestValidateClientToken_Malformed(t *testing.T) {
	_, err := auth.ValidateClientToken("not.a.jwt", testAccessKey)
	if err == nil {
		t.Error("expected error for malformed token, got nil")
	}
}
