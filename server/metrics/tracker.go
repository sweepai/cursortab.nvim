package metrics

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cursortab/logger"
)

const metricsURL = "https://backend.app.sweep.dev/backend/track_autocomplete_metrics"

const (
	EventShown    = "autocomplete_suggestion_shown"
	EventAccepted = "autocomplete_suggestion_accepted"
	EventDisposed = "autocomplete_suggestion_disposed"
)

const (
	SuggestionGhostText = "GHOST_TEXT"
)

type MetricsRequest struct {
	EventType          string `json:"event_type"`
	SuggestionType     string `json:"suggestion_type"`
	Additions          int    `json:"additions"`
	Deletions          int    `json:"deletions"`
	AutocompleteID     string `json:"autocomplete_id"`
	Lifespan           *int64 `json:"lifespan"`
	DebugInfo          string `json:"debug_info"`
	DeviceID           string `json:"device_id"`
	PrivacyModeEnabled bool   `json:"privacy_mode_enabled"`
}

type CompletionMetrics struct {
	ID        string
	Additions int
	Deletions int
	ShownAt   time.Time
}

type MetricsTracker struct {
	apiKey     string
	editorInfo string
	deviceID   string
	httpClient *http.Client
}

func NewTracker(apiKey, editorInfo, dataDir string) *MetricsTracker {
	deviceID := loadOrCreateDeviceID(dataDir)
	return &MetricsTracker{
		apiKey:     apiKey,
		editorInfo: editorInfo,
		deviceID:   deviceID,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (t *MetricsTracker) TrackShown(m *CompletionMetrics) {
	t.sendRequest(&MetricsRequest{
		EventType:          EventShown,
		SuggestionType:     SuggestionGhostText,
		Additions:          m.Additions,
		Deletions:          m.Deletions,
		AutocompleteID:     m.ID,
		Lifespan:           nil,
		DebugInfo:          t.editorInfo,
		DeviceID:           t.deviceID,
		PrivacyModeEnabled: false,
	})
}

func (t *MetricsTracker) TrackAccepted(m *CompletionMetrics) {
	t.sendRequest(&MetricsRequest{
		EventType:          EventAccepted,
		SuggestionType:     SuggestionGhostText,
		Additions:          m.Additions,
		Deletions:          m.Deletions,
		AutocompleteID:     m.ID,
		Lifespan:           nil,
		DebugInfo:          t.editorInfo,
		DeviceID:           t.deviceID,
		PrivacyModeEnabled: false,
	})
}

func (t *MetricsTracker) TrackDisposed(m *CompletionMetrics) {
	lifespan := time.Since(m.ShownAt).Milliseconds()
	t.sendRequest(&MetricsRequest{
		EventType:          EventDisposed,
		SuggestionType:     SuggestionGhostText,
		Additions:          m.Additions,
		Deletions:          m.Deletions,
		AutocompleteID:     m.ID,
		Lifespan:           &lifespan,
		DebugInfo:          t.editorInfo,
		DeviceID:           t.deviceID,
		PrivacyModeEnabled: false,
	})
}

func (t *MetricsTracker) sendRequest(req *MetricsRequest) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		body, err := json.Marshal(req)
		if err != nil {
			logger.Debug("metrics: marshal error: %v", err)
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", metricsURL, bytes.NewReader(body))
		if err != nil {
			logger.Debug("metrics: create request error: %v", err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+t.apiKey)

		resp, err := t.httpClient.Do(httpReq)
		if err != nil {
			logger.Debug("metrics: send error: %v", err)
			return
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)

		if resp.StatusCode >= 400 {
			logger.Debug("metrics: server returned %d for %s", resp.StatusCode, req.EventType)
		} else {
			logger.Debug("metrics: sent %s (id=%s)", req.EventType, req.AutocompleteID)
		}
	}()
}

func loadOrCreateDeviceID(dataDir string) string {
	if dataDir == "" {
		return GenerateUUID()
	}

	idPath := filepath.Join(dataDir, "device_id")

	data, err := os.ReadFile(idPath)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}

	id := GenerateUUID()
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logger.Warn("metrics: could not create data dir %s: %v", dataDir, err)
		return id
	}
	if err := os.WriteFile(idPath, []byte(id), 0644); err != nil {
		logger.Warn("metrics: could not write device_id: %v", err)
	}
	return id
}

func GenerateUUID() string {
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // variant 2
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}
