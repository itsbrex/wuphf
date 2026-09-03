package team

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (b *Broker) handleInterview(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		b.handleGetInterview(w, r)
	case http.MethodPost:
		b.handlePostInterview(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (b *Broker) handlePostInterview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From          string            `json:"from"`
		Channel       string            `json:"channel"`
		Question      string            `json:"question"`
		Context       string            `json:"context"`
		Options       []interviewOption `json:"options"`
		RecommendedID string            `json:"recommended_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.From) == "" || strings.TrimSpace(body.Question) == "" {
		http.Error(w, "from and question required", http.StatusBadRequest)
		return
	}
	reqBody, _ := json.Marshal(map[string]any{
		"action":         "create",
		"kind":           "interview",
		"title":          "Human interview",
		"from":           body.From,
		"channel":        body.Channel,
		"question":       body.Question,
		"context":        body.Context,
		"options":        body.Options,
		"recommended_id": body.RecommendedID,
		"blocking":       false,
		"required":       false,
	})
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(reqBody))
	b.handlePostRequest(w, r2)
}

func (b *Broker) handleGetInterview(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	pending := firstActiveHumanInterview(b.requests)
	if pending == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"pending": nil})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"pending": pending})
}

func (b *Broker) handleInterviewAnswer(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		b.handleGetInterviewAnswer(w, r)
	case http.MethodPost:
		b.handlePostInterviewAnswer(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (b *Broker) handleGetInterviewAnswer(w http.ResponseWriter, r *http.Request) {
	b.handleGetRequestAnswer(w, r)
}

func (b *Broker) handlePostInterviewAnswer(w http.ResponseWriter, r *http.Request) {
	b.handlePostRequestAnswer(w, r)
}
