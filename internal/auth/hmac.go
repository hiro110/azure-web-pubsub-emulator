package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	hmacScheme      = "HMAC-SHA256"
	TimestampWindow = 5 * time.Minute
)

// ValidateRequest validates the HMAC-SHA256 Authorization header of an HTTP request.
// It reads the body to verify the content hash, then restores it for downstream handlers.
func ValidateRequest(r *http.Request, accessKey []byte) error {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return fmt.Errorf("missing Authorization header")
	}
	if !strings.HasPrefix(authHeader, hmacScheme+" ") {
		return fmt.Errorf("unsupported authorization scheme")
	}

	signedHeaders, signature, err := parseAuthParams(authHeader[len(hmacScheme)+1:])
	if err != nil {
		return fmt.Errorf("malformed Authorization header: %w", err)
	}

	// Validate timestamp
	dateStr := r.Header.Get("x-ms-date")
	if dateStr == "" {
		dateStr = r.Header.Get("Date")
	}
	if err := validateTimestamp(dateStr, time.Now()); err != nil {
		return err
	}

	// Read and restore body
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			return fmt.Errorf("reading body: %w", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	contentHash := computeContentHash(bodyBytes)

	// If x-ms-content-sha256 is present, verify it matches the body
	if h := r.Header.Get("x-ms-content-sha256"); h != "" && h != contentHash {
		return fmt.Errorf("content hash mismatch")
	}

	headerValues := buildHeaderValues(r, signedHeaders, contentHash)
	stringToSign := r.Method + "\n" + r.URL.RequestURI() + "\n" + headerValues
	expected := computeSignature(accessKey, stringToSign)

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// SignRequest adds HMAC-SHA256 authorization headers to a request.
// Used for testing and for the emulator to sign outbound webhook requests.
func SignRequest(r *http.Request, accessKey []byte) error {
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			return fmt.Errorf("reading body: %w", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	contentHash := computeContentHash(bodyBytes)
	date := time.Now().UTC().Format(http.TimeFormat)

	r.Header.Set("x-ms-date", date)
	r.Header.Set("x-ms-content-sha256", contentHash)

	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}

	headerValues := date + ";" + host + ";" + contentHash
	stringToSign := r.Method + "\n" + r.URL.RequestURI() + "\n" + headerValues
	signature := computeSignature(accessKey, stringToSign)

	r.Header.Set("Authorization", fmt.Sprintf(
		"HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=%s",
		signature,
	))
	return nil
}

func parseAuthParams(params string) (signedHeaders, signature string, err error) {
	for _, part := range strings.Split(params, "&") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "SignedHeaders":
			signedHeaders = kv[1]
		case "Signature":
			signature = kv[1]
		}
	}
	if signedHeaders == "" || signature == "" {
		return "", "", fmt.Errorf("SignedHeaders and Signature are required")
	}
	return signedHeaders, signature, nil
}

func validateTimestamp(dateStr string, now time.Time) error {
	if dateStr == "" {
		return fmt.Errorf("missing date header (x-ms-date or Date)")
	}
	t, err := http.ParseTime(dateStr)
	if err != nil {
		return fmt.Errorf("invalid date header %q: %w", dateStr, err)
	}
	diff := now.Sub(t)
	if diff < -TimestampWindow || diff > TimestampWindow {
		return fmt.Errorf("timestamp outside allowed window (±%s)", TimestampWindow)
	}
	return nil
}

func buildHeaderValues(r *http.Request, signedHeaders, contentHash string) string {
	headers := strings.Split(signedHeaders, ";")
	values := make([]string, len(headers))
	for i, h := range headers {
		switch h {
		case "host":
			values[i] = r.Host
		case "x-ms-content-sha256":
			values[i] = contentHash
		default:
			values[i] = r.Header.Get(h)
		}
	}
	return strings.Join(values, ";")
}

func computeContentHash(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func computeSignature(key []byte, stringToSign string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
