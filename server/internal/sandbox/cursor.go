package sandbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cursorPayload struct {
	Version   int    `json:"v"`
	SessionID string `json:"sid"`
	StartedAt string `json:"ts"`
	ExecID    string `json:"eid"`
	Limit     int    `json:"l,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
}

func buildCursorToken(
	sessionID string,
	startedAt string,
	execID string,
	limit int,
	secret string,
	ttlSeconds int,
) (string, error) {
	if strings.TrimSpace(startedAt) == "" || strings.TrimSpace(execID) == "" {
		return "", nil
	}
	if strings.TrimSpace(secret) == "" {
		return "", errors.New("cursor secret is required")
	}
	now := time.Now().Unix()
	payload := cursorPayload{
		Version:   1,
		SessionID: sessionID,
		StartedAt: startedAt,
		ExecID:    execID,
		Limit:     limit,
		IssuedAt:  now,
	}
	if ttlSeconds > 0 {
		payload.ExpiresAt = now + int64(ttlSeconds)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal cursor payload: %w", err)
	}
	bodyPart := base64.RawURLEncoding.EncodeToString(body)
	sig := signCursor(body, secret)
	sigPart := base64.RawURLEncoding.EncodeToString(sig)
	return bodyPart + "." + sigPart, nil
}

func parseCursor(
	sessionID string,
	cursor string,
	limit int,
	secret string,
	ttlSeconds int,
) (string, string, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return "", "", nil
	}
	// Backward compatible cursor: started_at|exec_id
	if strings.Contains(cursor, "|") {
		parts := strings.SplitN(cursor, "|", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return "", "", errors.New("invalid legacy cursor")
		}
		return parts[0], parts[1], nil
	}
	if strings.TrimSpace(secret) == "" {
		return "", "", errors.New("cursor secret is required")
	}
	payload, err := parseCursorToken(cursor, secret)
	if err != nil {
		return "", "", err
	}
	if payload.SessionID != sessionID {
		return "", "", errors.New("cursor session mismatch")
	}
	if payload.ExpiresAt > 0 && time.Now().Unix() > payload.ExpiresAt {
		return "", "", errors.New("cursor expired")
	}
	if ttlSeconds > 0 && payload.IssuedAt > 0 && time.Now().Unix()-payload.IssuedAt > int64(ttlSeconds)*2 {
		return "", "", errors.New("cursor too old")
	}
	if payload.StartedAt == "" || payload.ExecID == "" {
		return "", "", errors.New("cursor missing fields")
	}
	if payload.Limit > 0 && limit > 0 && payload.Limit != limit {
		return "", "", errors.New("cursor limit mismatch")
	}
	return payload.StartedAt, payload.ExecID, nil
}

func parseCursorToken(token, secret string) (cursorPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return cursorPayload{}, errors.New("invalid cursor token format")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursorPayload{}, fmt.Errorf("decode cursor payload: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursorPayload{}, fmt.Errorf("decode cursor signature: %w", err)
	}
	expected := signCursor(body, secret)
	if !hmac.Equal(signature, expected) {
		return cursorPayload{}, errors.New("cursor signature mismatch")
	}

	var payload cursorPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return cursorPayload{}, fmt.Errorf("unmarshal cursor payload: %w", err)
	}
	if payload.Version != 1 {
		return cursorPayload{}, errors.New("unsupported cursor version: " + strconv.Itoa(payload.Version))
	}
	return payload, nil
}

func signCursor(body []byte, secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}
