/*
Copyright © 2026 Outlander Connect
FCM (Firebase Cloud Messaging HTTP v1) client for Android Live Ongoing Notifications
*/
package cmd

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type GoogleServiceAccount struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	ClientID     string `json:"client_id"`
	AuthURI      string `json:"auth_uri"`
	TokenURI     string `json:"token_uri"`
}

type FCMClient struct {
	mu           sync.Mutex
	sa           *GoogleServiceAccount
	rsaKey       *rsa.PrivateKey
	cachedToken  string
	tokenExpires time.Time
	httpClient   *http.Client
}

// NewFCMClient attempts to initialize FCM client from service account json file
func NewFCMClient(customPath string) *FCMClient {
	candidates := []string{
		customPath,
		"/etc/phev2mqtt/firebase_service_account.json",
		"/home/rinszky/phev2mqtt/firebase_service_account.json",
		filepath.Join(os.TempDir(), "firebase_service_account.json"),
	}

	var jsonBytes []byte
	var loadedPath string
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			jsonBytes = data
			loadedPath = p
			break
		}
	}

	if len(jsonBytes) == 0 {
		log.Infof("[FCM] No firebase_service_account.json found in candidate paths, FCM push disabled")
		return nil
	}

	var sa GoogleServiceAccount
	if err := json.Unmarshal(jsonBytes, &sa); err != nil {
		log.Warnf("[FCM] Failed to parse %s: %v", loadedPath, err)
		return nil
	}

	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		log.Warnf("[FCM] Failed to decode PEM private key from %s", loadedPath)
		return nil
	}

	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Fallback to PKCS1 if needed
		if pkcs1, err1 := x509.ParsePKCS1PrivateKey(block.Bytes); err1 == nil {
			privKey = pkcs1
		} else {
			log.Warnf("[FCM] Failed to parse private key: %v", err)
			return nil
		}
	}

	rsaKey, ok := privKey.(*rsa.PrivateKey)
	if !ok {
		log.Warnf("[FCM] Private key in %s is not RSA", loadedPath)
		return nil
	}

	log.Infof("[FCM] Initialized FCM HTTP v1 client from %s (project=%s, client=%s)", loadedPath, sa.ProjectID, sa.ClientEmail)

	return &FCMClient{
		sa:         &sa,
		rsaKey:     rsaKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (f *FCMClient) getAccessToken() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cachedToken != "" && time.Now().Before(f.tokenExpires) {
		return f.cachedToken, nil
	}

	if f.rsaKey == nil || f.sa == nil {
		return "", fmt.Errorf("FCM service account not configured")
	}

	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claimsJSON, _ := json.Marshal(map[string]interface{}{
		"iss":   f.sa.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   "https://oauth2.googleapis.com/token",
		"exp":   now + 3600,
		"iat":   now,
	})
	claims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	toSign := header + "." + claims

	h := sha256.Sum256([]byte(toSign))
	sigBytes, err := rsa.SignPKCS1v15(rand.Reader, f.rsaKey, crypto.SHA256, h[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign OAuth2 JWT: %w", err)
	}
	sig := base64.RawURLEncoding.EncodeToString(sigBytes)
	assertion := toSign + "." + sig

	tokenURL := f.sa.TokenURI
	if tokenURL == "" {
		tokenURL = "https://oauth2.googleapis.com/token"
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	resp, err := f.httpClient.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to request OAuth2 token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OAuth2 token endpoint returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var res map[string]interface{}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("failed to parse OAuth2 token response: %w", err)
	}

	token, ok := res["access_token"].(string)
	if !ok || token == "" {
		return "", fmt.Errorf("no access_token in OAuth2 response: %s", string(body))
	}

	f.cachedToken = token
	f.tokenExpires = time.Now().Add(50 * time.Minute)
	log.Infof("[FCM] Successfully refreshed OAuth2 access token for project %s", f.sa.ProjectID)
	return f.cachedToken, nil
}

func (f *FCMClient) SendDataPush(targetToken string, data map[string]string) error {
	if f == nil || targetToken == "" {
		return nil
	}

	accessToken, err := f.getAccessToken()
	if err != nil {
		return err
	}

	reqURL := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", f.sa.ProjectID)

	payload := map[string]interface{}{
		"message": map[string]interface{}{
			"token": targetToken,
			"data":  data,
			"android": map[string]interface{}{
				"priority": "HIGH",
			},
		},
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json; UTF-8")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("FCM HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	log.Infof("[FCM] Push notification delivered successfully (HTTP 200, token=%.16s...)", targetToken)
	return nil
}

func (f *FCMClient) UpdateClimate(targetToken string, mode, modeName string, remainingMins, duration int, stopTime string, isActive bool) error {
	event := "update"
	if isActive && remainingMins >= duration-1 {
		event = "start"
	}
	return f.SendDataPush(targetToken, map[string]string{
		"type":          "climate",
		"event":         event,
		"mode":          mode,
		"modeName":      modeName,
		"remainingMins": fmt.Sprintf("%d", remainingMins),
		"duration":      fmt.Sprintf("%d", duration),
		"stopTime":      stopTime,
		"isActive":      fmt.Sprintf("%v", isActive),
		"lang":          "hu",
	})
}

func (f *FCMClient) EndClimate(targetToken string) error {
	return f.SendDataPush(targetToken, map[string]string{
		"type":  "climate",
		"event": "end",
	})
}

func (f *FCMClient) UpdateCharging(targetToken string, soc, remainingMins int, completionTime string) error {
	return f.SendDataPush(targetToken, map[string]string{
		"type":           "charging",
		"event":          "update",
		"soc":            fmt.Sprintf("%d", soc),
		"remainingMins":  fmt.Sprintf("%d", remainingMins),
		"completionTime": completionTime,
		"lang":           "hu",
	})
}

func (f *FCMClient) EndCharging(targetToken string) error {
	return f.SendDataPush(targetToken, map[string]string{
		"type":  "charging",
		"event": "end",
	})
}
