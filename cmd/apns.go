/*
Copyright © 2026 Outlander Connect
APNs (Apple Push Notification service) client for Outlander PHEV Live Activities
*/
package cmd

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type APNsTokens struct {
	Charging            string `json:"charging"`
	Climate             string `json:"climate"`
	PushToStartCharging string `json:"push_to_start_charging"`
	PushToStartClimate  string `json:"push_to_start_climate"`
}

type APNsClient struct {
	mu            sync.Mutex
	privateKey    *ecdsa.PrivateKey
	keyID         string
	teamID        string
	bundleID      string
	cachedJWT     string
	jwtExpiresAt  time.Time
	tokens        APNsTokens
	tokenFilePath string

	httpClient         *http.Client
	isSandboxToken     map[string]bool
	isProductionToken  map[string]bool

	// Rate limiting & state tracking
	lastChargingUpdate time.Time
	lastChargingSOC    int
	lastChargingRem    int
	lastChargingState  bool

	lastClimateUpdate  time.Time
	lastClimateMode    string
	lastClimateRem     int
	lastClimateState   bool
}

func newAPNsClient() *APNsClient {
	keyID := viper.GetString("apns_key_id")
	if keyID == "" {
		keyID = "34BYS72AWT"
	}
	teamID := viper.GetString("apns_team_id")
	if teamID == "" {
		teamID = "CSMCPWWS2A"
	}
	bundleID := viper.GetString("apns_bundle_id")
	if bundleID == "" {
		bundleID = "com.outlander.connect"
	}

	keyFile := viper.GetString("apns_key_file")
	if keyFile == "" {
		// Look for apns_key.p8 or AuthKey_*.p8 in working dir or /etc/phev2mqtt/
		candidates := []string{
			"apns_key.p8",
			"AuthKey_34BYS72AWT.p8",
			"/etc/phev2mqtt/apns_key.p8",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				keyFile = c
				break
			}
		}
	}

	var privateKey *ecdsa.PrivateKey
	if keyFile != "" {
		data, err := os.ReadFile(keyFile)
		if err != nil {
			log.Warnf("[APNs] Failed to read key file %s: %v", keyFile, err)
		} else {
			key, err := parseAPNsPrivateKey(data)
			if err != nil {
				log.Warnf("[APNs] Failed to parse private key %s: %v", keyFile, err)
			} else {
				privateKey = key
				log.Infof("[APNs] Initialized APNs client with key %s (KeyID=%s, TeamID=%s)", keyFile, keyID, teamID)
			}
		}
	} else {
		log.Debug("[APNs] No APNs key file configured, APNs push will be disabled until configured")
	}

	tokenFile := filepath.Join(os.TempDir(), "phev_apns_tokens.json")
	client := &APNsClient{
		privateKey:        privateKey,
		keyID:             keyID,
		teamID:            teamID,
		bundleID:          bundleID,
		tokenFilePath:     tokenFile,
		isSandboxToken:    make(map[string]bool),
		isProductionToken: make(map[string]bool),
		httpClient: &http.Client{
			Transport: &http.Transport{
				ForceAttemptHTTP2: true,
			},
			Timeout: 10 * time.Second,
		},
	}

	client.loadTokens()
	return client
}

func parseAPNsPrivateKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM data found in key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ParsePKCS8PrivateKey failed: %w", err)
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not an ECDSA private key")
	}
	return ecKey, nil
}

func (a *APNsClient) loadTokens() {
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := os.ReadFile(a.tokenFilePath)
	if err == nil {
		var tokens APNsTokens
		if err := json.Unmarshal(data, &tokens); err == nil {
			a.tokens = tokens
			log.Infof("[APNs] Loaded stored push tokens from %s", a.tokenFilePath)
		}
	}
}

func (a *APNsClient) saveTokens() {
	data, err := json.MarshalIndent(a.tokens, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(a.tokenFilePath, data, 0600)
}

func (a *APNsClient) SetToken(tokenType string, token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	changed := false
	normalizedType := strings.TrimSuffix(strings.ToLower(tokenType), "_token")
	switch normalizedType {
	case "charging":
		if a.tokens.Charging != token {
			a.tokens.Charging = token
			changed = true
		}
	case "climate":
		if a.tokens.Climate != token {
			a.tokens.Climate = token
			changed = true
		}
	case "push_to_start_charging", "push_to_start":
		if a.tokens.PushToStartCharging != token {
			a.tokens.PushToStartCharging = token
			changed = true
		}
	case "push_to_start_climate":
		if a.tokens.PushToStartClimate != token {
			a.tokens.PushToStartClimate = token
			changed = true
		}
	default:
		// Try parsing as JSON object
		var parsed map[string]string
		if err := json.Unmarshal([]byte(token), &parsed); err == nil {
			if v, ok := parsed["charging"]; ok && v != "" {
				a.tokens.Charging = v
				changed = true
			}
			if v, ok := parsed["climate"]; ok && v != "" {
				a.tokens.Climate = v
				changed = true
			}
		}
	}

	if changed {
		a.saveTokens()
		log.Infof("[APNs] Updated and persisted %s push token: %s", tokenType, token)
	}
}

func (a *APNsClient) generateJWT() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.privateKey == nil {
		return "", fmt.Errorf("no APNs private key loaded")
	}

	if a.cachedJWT != "" && time.Now().Before(a.jwtExpiresAt.Add(-5*time.Minute)) {
		return a.cachedJWT, nil
	}

	header := map[string]string{
		"alg": "ES256",
		"kid": a.keyID,
	}
	headerJSON, _ := json.Marshal(header)

	now := time.Now().Unix()
	claims := map[string]interface{}{
		"iss": a.teamID,
		"iat": now,
	}
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, a.privateKey, hash[:])
	if err != nil {
		return "", fmt.Errorf("ecdsa sign failed: %w", err)
	}

	sigBytes := formatSignature(r, s)
	sigB64 := base64.RawURLEncoding.EncodeToString(sigBytes)

	jwt := signingInput + "." + sigB64
	a.cachedJWT = jwt
	a.jwtExpiresAt = time.Now().Add(50 * time.Minute)
	return jwt, nil
}

func formatSignature(r, s *big.Int) []byte {
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	if len(rBytes) > 32 {
		rBytes = rBytes[len(rBytes)-32:]
	}
	if len(sBytes) > 32 {
		sBytes = sBytes[len(sBytes)-32:]
	}
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):64], sBytes)
	return sig
}

func (a *APNsClient) sendPush(deviceToken string, payload []byte) error {
	jwt, err := a.generateJWT()
	if err != nil {
		return err
	}

	endpoints := []string{
		"https://api.sandbox.push.apple.com:443/3/device/",
		"https://api.push.apple.com:443/3/device/",
	}

	a.mu.Lock()
	if a.isProductionToken[deviceToken] {
		endpoints = []string{
			"https://api.push.apple.com:443/3/device/",
			"https://api.sandbox.push.apple.com:443/3/device/",
		}
	}
	a.mu.Unlock()

	topic := a.bundleID + ".push-type.liveactivity"
	var lastErr error

	for _, baseURL := range endpoints {
		url := baseURL + deviceToken
		req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", "bearer "+jwt)
		req.Header.Set("apns-topic", topic)
		req.Header.Set("apns-push-type", "liveactivity")
		req.Header.Set("apns-priority", "10")
		req.Header.Set("Content-Type", "application/json; charset=utf-8")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			a.mu.Lock()
			if strings.Contains(baseURL, "sandbox") {
				a.isSandboxToken[deviceToken] = true
			} else {
				a.isProductionToken[deviceToken] = true
			}
			a.mu.Unlock()
			log.Infof("[APNs] Push notification delivered (HTTP 200, env=%s, apns-id=%s)", baseURL, resp.Header.Get("apns-id"))
			return nil
		}

		log.Debugf("[APNs] Push to %s returned %d: %s", baseURL, resp.StatusCode, string(respBody))
		lastErr = fmt.Errorf("APNs error %d: %s", resp.StatusCode, string(respBody))
	}

	return lastErr
}

// UpdateCharging sends a Live Activity update for battery charging
func (a *APNsClient) UpdateCharging(soc int, remainingMins int, completionTime string, isCharging bool) {
	if a.privateKey == nil {
		return
	}

	a.mu.Lock()
	token := a.tokens.Charging
	startToken := a.tokens.PushToStartCharging
	now := time.Now()

	// Rate limiting: at most once every 5 seconds unless state or SOC changed
	if a.lastChargingState == isCharging && a.lastChargingSOC == soc && a.lastChargingRem == remainingMins && now.Sub(a.lastChargingUpdate) < 30*time.Second {
		a.mu.Unlock()
		return
	}

	a.lastChargingUpdate = now
	a.lastChargingSOC = soc
	a.lastChargingRem = remainingMins
	a.lastChargingState = isCharging
	a.mu.Unlock()

	targetToken := token
	event := "update"
	if targetToken == "" && startToken != "" {
		targetToken = startToken
		event = "start"
	}

	if targetToken == "" {
		log.Debug("[APNs] No charging push token registered, skipping push")
		return
	}

	startDate := now.Unix()
	endDate := now.Add(time.Duration(remainingMins) * time.Minute).Unix()
	if remainingMins <= 0 {
		endDate = startDate
	}

	payloadMap := map[string]interface{}{
		"aps": map[string]interface{}{
			"timestamp": startDate,
			"event":     event,
			"content-state": map[string]interface{}{
				"soc":            soc,
				"remainingMins":  remainingMins,
				"completionTime": completionTime,
				"isCharging":     isCharging,
				"lang":           "hu",
				"startDate":      startDate,
				"endDate":        endDate,
			},
		},
	}

	if event == "start" {
		payloadMap["aps"].(map[string]interface{})["attributes-type"] = "ChargingActivityAttributes"
		payloadMap["aps"].(map[string]interface{})["attributes"] = map[string]interface{}{
			"title": "Outlander PHEV",
		}
	}

	data, _ := json.Marshal(payloadMap)
	go func() {
		if err := a.sendPush(targetToken, data); err != nil {
			log.Warnf("[APNs] Failed to send charging push: %v", err)
		}
	}()
}

// EndCharging dismisses the charging Live Activity
func (a *APNsClient) EndCharging(soc int) {
	if a.privateKey == nil {
		return
	}

	a.mu.Lock()
	token := a.tokens.Charging
	a.lastChargingState = false
	a.tokens.Charging = ""
	a.saveTokens()
	a.mu.Unlock()

	if token == "" {
		return
	}

	now := time.Now().Unix()
	payloadMap := map[string]interface{}{
		"aps": map[string]interface{}{
			"timestamp":      now,
			"event":          "end",
			"dismissal-date": now,
			"content-state": map[string]interface{}{
				"soc":            soc,
				"remainingMins":  0,
				"completionTime": "Kész",
				"isCharging":     false,
				"lang":           "hu",
				"startDate":      now,
				"endDate":        now,
			},
		},
	}

	data, _ := json.Marshal(payloadMap)
	go func() {
		if err := a.sendPush(token, data); err != nil {
			log.Warnf("[APNs] Failed to send charging end push: %v", err)
		}
	}()
}

// UpdateClimate sends a Live Activity update for HVAC / preconditioning
func (a *APNsClient) UpdateClimate(mode string, modeName string, remainingMins int, duration int, stopTime string, isActive bool) {
	if a.privateKey == nil {
		return
	}

	a.mu.Lock()
	token := a.tokens.Climate
	startToken := a.tokens.PushToStartClimate
	now := time.Now()

	if a.lastClimateState == isActive && a.lastClimateMode == mode && a.lastClimateRem == remainingMins && now.Sub(a.lastClimateUpdate) < 30*time.Second {
		a.mu.Unlock()
		return
	}

	a.lastClimateUpdate = now
	a.lastClimateMode = mode
	a.lastClimateRem = remainingMins
	a.lastClimateState = isActive
	a.mu.Unlock()

	targetToken := token
	event := "update"
	if targetToken == "" && startToken != "" {
		targetToken = startToken
		event = "start"
	}

	if targetToken == "" {
		log.Debug("[APNs] No climate push token registered, skipping push")
		return
	}

	startDate := now.Unix()
	endDate := now.Add(time.Duration(remainingMins) * time.Minute).Unix()

	payloadMap := map[string]interface{}{
		"aps": map[string]interface{}{
			"timestamp": startDate,
			"event":     event,
			"content-state": map[string]interface{}{
				"mode":          mode,
				"modeName":      modeName,
				"remainingMins": remainingMins,
				"duration":      duration,
				"stopTime":      stopTime,
				"isActive":      isActive,
				"lang":          "hu",
				"startDate":     startDate,
				"endDate":       endDate,
			},
		},
	}

	if event == "start" {
		payloadMap["aps"].(map[string]interface{})["attributes-type"] = "ClimateActivityAttributes"
		payloadMap["aps"].(map[string]interface{})["attributes"] = map[string]interface{}{
			"title": "Outlander Klíma",
		}
	}

	data, _ := json.Marshal(payloadMap)
	go func() {
		if err := a.sendPush(targetToken, data); err != nil {
			log.Warnf("[APNs] Failed to send climate push: %v", err)
		}
	}()
}

// EndClimate dismisses the climate Live Activity
func (a *APNsClient) EndClimate(mode string, modeName string) {
	if a.privateKey == nil {
		return
	}

	a.mu.Lock()
	token := a.tokens.Climate
	a.lastClimateState = false
	a.tokens.Climate = ""
	a.saveTokens()
	a.mu.Unlock()

	if token == "" {
		return
	}

	now := time.Now().Unix()
	payloadMap := map[string]interface{}{
		"aps": map[string]interface{}{
			"timestamp":      now,
			"event":          "end",
			"dismissal-date": now,
			"content-state": map[string]interface{}{
				"mode":          mode,
				"modeName":      modeName,
				"remainingMins": 0,
				"duration":      10,
				"stopTime":      "--:--",
				"isActive":      false,
				"lang":          "hu",
				"startDate":     now,
				"endDate":       now,
			},
		},
	}

	data, _ := json.Marshal(payloadMap)
	go func() {
		if err := a.sendPush(token, data); err != nil {
			log.Warnf("[APNs] Failed to send climate end push: %v", err)
		}
	}()
}
