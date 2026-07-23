package api

import (
	"encoding/json"
	"net/http"
)

type Response struct {
	Code      int    `json:"code"`
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
}

func writeCodedError(w http.ResponseWriter, status int, errorCode, message string, data any) {
	if message == "" {
		message = http.StatusText(status)
	}
	writeJSON(w, status, Response{
		Code:      status,
		ErrorCode: errorCode,
		Message:   message,
		Data:      data,
	})
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		Data:    data,
	})
}

func writeError(w http.ResponseWriter, status int, message string) {
	if message == "" {
		message = http.StatusText(status)
	}
	writeJSON(w, status, Response{
		Code:    status,
		Message: message,
		Data:    nil,
	})
}

func writeJSON(w http.ResponseWriter, status int, response Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
