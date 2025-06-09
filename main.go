package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PrivateGPT API Configuration
const (
	PRIVATEGPT_HOST = "http://localhost:8001"
	SERVER_PORT     = ":8080"
	STATIC_DIR      = "./static" // HTML файлы
)

// CORS middleware для правильной работы с браузером
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Разрешить все origins для development (в продакшене укажите конкретный домен)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Logging middleware
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// PrivateGPT API Proxy Handler
func createAPIProxy() http.Handler {
	target, err := url.Parse(PRIVATEGPT_HOST)
	if err != nil {
		log.Fatal("Invalid PrivateGPT URL:", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	
	// Кастомизируем прокси для лучшей работы
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Добавляем CORS заголовки к ответам PrivateGPT
		resp.Header.Set("Access-Control-Allow-Origin", "*")
		resp.Header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		resp.Header.Set("Access-Control-Expose-Headers", "Content-Length, Content-Type")
		return nil
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Proxy error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "PrivateGPT service unavailable",
			"details": err.Error(),
		})
	}

	return proxy
}

// Static files handler для HTML файла
func staticFilesHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	
	// Если путь "/" или пустой - возвращаем index.html
	if path == "/" || path == "" {
		path = "/index.html"
	}
	
	// Удаляем ведущий слэш для filepath.Join
	if strings.HasPrefix(path, "/") {
		path = path[1:]
	}
	
	fullPath := filepath.Join(STATIC_DIR, path)
	
	// Проверяем существование файла
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		// Если файл не найден - возвращаем index.html
		fullPath = filepath.Join(STATIC_DIR, "index.html")
	}
	
	// Устанавливаем правильный Content-Type
	ext := filepath.Ext(fullPath)
	switch ext {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	}
	
	http.ServeFile(w, r, fullPath)
}

// File upload handler - убираем, так как HTML использует прямой API
// Вместо этого все запросы /v1/ingest/file проксируются напрямую
func healthHandler(w http.ResponseWriter, r *http.Request) {
	// Проверяем доступность PrivateGPT
	resp, err := http.Get(PRIVATEGPT_HOST + "/health")
	
	health := map[string]interface{}{
		"bridge": "ok",
		"timestamp": time.Now().Unix(),
	}
	
	if err != nil {
		health["privategpt"] = "unavailable"
		health["error"] = err.Error()
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			health["privategpt"] = "ok"
		} else {
			health["privategpt"] = "error"
			health["status_code"] = resp.StatusCode
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// WebSocket proxy для streaming (если нужно)
func websocketProxy(w http.ResponseWriter, r *http.Request) {
	// TODO: Добавить WebSocket проксирование если PrivateGPT поддерживает streaming
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte("WebSocket streaming not implemented yet"))
}

func main() {
	log.Printf("Starting PrivateGPT Bridge Server...")
	log.Printf("PrivateGPT API: %s", PRIVATEGPT_HOST)
	log.Printf("Static files: %s", STATIC_DIR)
	log.Printf("Server port: %s", SERVER_PORT)

	// Создаем API прокси
	apiProxy := createAPIProxy()

	// Настраиваем маршруты
	mux := http.NewServeMux()

	// API маршруты - проксируем к PrivateGPT
	mux.Handle("/v1/", apiProxy)
	mux.Handle("/api/", apiProxy)
	
	// Health check - сначала проверяем наш прокси, потом PrivateGPT
	mux.Handle("/health", http.HandlerFunc(healthHandler))
	
	// Специальные эндпоинты
	mux.Handle("/ws", http.HandlerFunc(websocketProxy))
	
	// Статичные файлы Next.js для всех остальных маршрутов
	mux.HandleFunc("/", staticFilesHandler)

	// Применяем middleware
	handler := corsMiddleware(loggingMiddleware(mux))

	// Запускаем сервер
	log.Printf("🚀 Server running on http://localhost%s", SERVER_PORT)
	log.Printf("📁 Serving HTML files from: %s", STATIC_DIR)
	log.Printf("🔄 Proxying API requests to: %s", PRIVATEGPT_HOST)
	
	if err := http.ListenAndServe(SERVER_PORT, handler); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
