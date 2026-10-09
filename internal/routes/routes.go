package routes

import (
	"net/http"
)

// RegisterRoutes registers all API endpoints with CORS support.
func RegisterRoutes(mux *http.ServeMux, handler *Handler) {
	mux.HandleFunc("/api/v1/classify", corsMiddleware(handler.HandleClassify))
	mux.HandleFunc("/classify", corsMiddleware(handler.HandleClassify))
	mux.HandleFunc("/api/v1/health", corsMiddleware(handler.HandleHealth))
	mux.HandleFunc("/health", corsMiddleware(handler.HandleHealth))
	mux.HandleFunc("/", corsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"message":"AI HSN Code Finder API is running","endpoint":"POST /api/v1/classify"}`))
			return
		}
		http.NotFound(w, r)
	}))
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, ngrok-skip-browser-warning")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}
