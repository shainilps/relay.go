package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/shainilps/relay/internal/services"
)

type Handler struct {
	service *services.RelayService
}

func NewHandler(service *services.RelayService) *Handler {
	return &Handler{service: service}
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, services.ErrInvalidTransaction):
		return http.StatusBadRequest
	case errors.Is(err, services.ErrOutOfFee):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (h *Handler) Broadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TxHex string `json:"txHex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	resp, err := h.service.Broadcast(r.Context(), req.TxHex)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) FundAndBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TxHex string `json:"txHex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	resp, err := h.service.FundAndBroadcast(r.Context(), req.TxHex)
	if err != nil {
		http.Error(w, err.Error(), errorStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) GetFundingAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr, err := h.service.GetFundingAddress()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"address": addr})
}
