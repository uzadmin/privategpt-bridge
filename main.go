package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	PRIVATEGPT_HOST = "http://localhost:8001" // PrivateGPT API
	SERVER_PORT     = ":8080"                 // Bridge server port
	MAX_FILE_SIZE   = 50 << 20                // 50MB
)

// PrivateGPT API Response structures
type IngestResponse struct {
	Object string          `json:"object"`
	Model  string          `json:"model"`
	Data   []IngestedFile  `json:"data"`
}

type IngestedFile struct {
	DocID      string                 `json:"doc_id"`
	DocMetadata map[string]interface{} `json:"doc_metadata,omitempty"`
}

type ListFilesResponse struct {
	Object string          `json:"object"`
	Model  string          `json:"model"`
	Data   []FileInfo      `json:"data"`
}

type FileInfo struct {
	DocID      string                 `json:"doc_id"`
	DocMetadata map[string]interface{} `json:"doc_metadata"`
}

type ChatRequest struct {
	Model            string      `json:"model"`
	Messages         []Message   `json:"messages"`
	UseContext       bool        `json:"use_context"`
	ContextFilter    *ContextFilter `json:"context_filter,omitempty"`
	IncludeSources   bool        `json:"include_sources"`
	Stream           bool        `json:"stream"`
	MaxTokens        int         `json:"max_tokens,omitempty"`
	Temperature      float64     `json:"temperature,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ContextFilter struct {
	DocsIds []string `json:"docs_ids,omitempty"`
}

type CompletionRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	UseContext  bool    `json:"use_context"`
	ContextFilter *ContextFilter `json:"context_filter,omitempty"`
	IncludeSources bool `json:"include_sources"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type ChunksRequest struct {
	Text        string   `json:"text"`
	ContextFilter *ContextFilter `json:"context_filter,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	PrevNextChunks int   `json:"prev_next_chunks,omitempty"`
}

type BridgeConfig struct {
	Mode         string   `json:"mode"`         // "rag", "search", "basic", "summarize"
	UseContext   bool     `json:"use_context"`
	SelectedDocs []string `json:"selected_docs"`
	MaxTokens    int      `json:"max_tokens"`
	Temperature  float64  `json:"temperature"`
}

// CORS middleware
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// Health check handler
func healthHandler(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get(PRIVATEGPT_HOST + "/health")
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error",
			"message": "PrivateGPT API is not available",
			"error": err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"message": "Bridge server is running",
		"privategpt_status": resp.StatusCode == 200,
	})
}

// File upload handler
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(MAX_FILE_SIZE)
	if err != nil {
		log.Printf("Error parsing multipart form: %v", err)
		http.Error(w, "File too large or invalid", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		log.Printf("Error getting file: %v", err)
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Check file extension
	allowedExts := map[string]bool{
		".pdf": true, ".docx": true, ".doc": true, ".txt": true,
		".md": true, ".html": true, ".csv": true, ".json": true,
		".pptx": true, ".ppt": true, ".epub": true, ".ipynb": true,
	}
	
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedExts[ext] {
		http.Error(w, "File type not supported", http.StatusBadRequest)
		return
	}

	// Create multipart form for PrivateGPT
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	
	fw, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		log.Printf("Error creating form file: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	
	_, err = io.Copy(fw, file)
	if err != nil {
		log.Printf("Error copying file: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	
	writer.Close()

	// Forward to PrivateGPT
	req, err := http.NewRequest("POST", PRIVATEGPT_HOST+"/v1/ingest/file", &buf)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	
	req.Header.Set("Content-Type", writer.FormDataContentType())
	
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error forwarding request: %v", err)
		http.Error(w, "PrivateGPT API error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	
	log.Printf("File uploaded: %s (%d bytes)", header.Filename, header.Size)
}

// List ingested files handler with deduplication
func listFilesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp, err := http.Get(PRIVATEGPT_HOST + "/v1/ingest/list")
	if err != nil {
		log.Printf("Error getting file list: %v", err)
		http.Error(w, "PrivateGPT API error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// Parse the response to deduplicate files
	var listResp ListFilesResponse
	err = json.NewDecoder(resp.Body).Decode(&listResp)
	if err != nil {
		log.Printf("Error parsing file list response: %v", err)
		http.Error(w, "Error parsing response", http.StatusInternalServerError)
		return
	}

	// Deduplicate files by filename and keep the most recent one
	fileMap := make(map[string]FileInfo)
	for _, file := range listResp.Data {
		fileName := "Unknown"
		if file.DocMetadata != nil {
			if name, ok := file.DocMetadata["file_name"].(string); ok {
				fileName = name
			}
		}
		
		// Use filename as key for deduplication
		// If file already exists, compare doc_id and keep the lexicographically larger one (likely newer)
		if existing, exists := fileMap[fileName]; exists {
			if file.DocID > existing.DocID {
				fileMap[fileName] = file
			}
		} else {
			fileMap[fileName] = file
		}
	}

	// Convert back to slice
	deduplicatedFiles := make([]FileInfo, 0, len(fileMap))
	for _, file := range fileMap {
		deduplicatedFiles = append(deduplicatedFiles, file)
	}

	// Create response
	deduplicatedResp := ListFilesResponse{
		Object: listResp.Object,
		Model:  listResp.Model,
		Data:   deduplicatedFiles,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(deduplicatedResp)
	
	log.Printf("File list returned: %d unique files (from %d total)", len(deduplicatedFiles), len(listResp.Data))
}

// Delete file handler
func deleteFileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract doc_id from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if path == "" {
		http.Error(w, "Document ID required", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest("DELETE", PRIVATEGPT_HOST+"/v1/ingest/"+path, nil)
	if err != nil {
		log.Printf("Error creating delete request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error deleting file: %v", err)
		http.Error(w, "PrivateGPT API error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if resp.StatusCode == 200 {
		json.NewEncoder(w).Encode(map[string]string{"message": "File deleted successfully"})
	} else {
		io.Copy(w, resp.Body)
	}
	
	log.Printf("File deleted: %s", path)
}

// Enhanced chat handler with mode support
func chatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var reqData struct {
		Message     string      `json:"message"`
		Config      BridgeConfig `json:"config"`
		SystemPrompt string     `json:"system_prompt,omitempty"`
		History     []Message   `json:"history,omitempty"`
	}

	err := json.NewDecoder(r.Body).Decode(&reqData)
	if err != nil {
		log.Printf("Error decoding request: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Log the received configuration for debugging
	log.Printf("Chat request - Mode: %s, UseContext: %t, SelectedDocs: %v", 
		reqData.Config.Mode, reqData.Config.UseContext, reqData.Config.SelectedDocs)

	var endpoint string
	var payload interface{}

	switch reqData.Config.Mode {
	case "search":
		// Use chunks endpoint for search
		endpoint = "/v1/chunks"
		chunksReq := ChunksRequest{
			Text:  reqData.Message,
			Limit: 10,
			PrevNextChunks: 1,
		}
		if len(reqData.Config.SelectedDocs) > 0 {
			chunksReq.ContextFilter = &ContextFilter{DocsIds: reqData.Config.SelectedDocs}
		}
		payload = chunksReq

	case "basic":
		// Use chat completions endpoint WITHOUT context - this is the key difference
		endpoint = "/v1/chat/completions"
		messages := []Message{}
		
		if reqData.SystemPrompt != "" {
			messages = append(messages, Message{Role: "system", Content: reqData.SystemPrompt})
		}
		
		// Add history but limit it for basic mode
		if len(reqData.History) > 4 { // Keep only last 2 exchanges
			messages = append(messages, reqData.History[len(reqData.History)-4:]...)
		} else {
			messages = append(messages, reqData.History...)
		}
		
		// Add current message
		messages = append(messages, Message{Role: "user", Content: reqData.Message})

		chatReq := ChatRequest{
			Model:         "private-gpt",
			Messages:      messages,
			UseContext:    false, // EXPLICITLY FALSE for basic mode
			IncludeSources: false, // No sources in basic mode
			Stream:        false,
			MaxTokens:     reqData.Config.MaxTokens,
			Temperature:   reqData.Config.Temperature,
		}
		// NO context filter for basic mode
		payload = chatReq

	case "summarize":
		// Use completions with context for summarization
		endpoint = "/v1/completions"
		prompt := fmt.Sprintf("Please provide a comprehensive summary of the following content: %s", reqData.Message)
		completionReq := CompletionRequest{
			Model:         "private-gpt",
			Prompt:        prompt,
			UseContext:    true,
			IncludeSources: true,
			MaxTokens:     reqData.Config.MaxTokens,
			Temperature:   reqData.Config.Temperature,
		}
		if len(reqData.Config.SelectedDocs) > 0 {
			completionReq.ContextFilter = &ContextFilter{DocsIds: reqData.Config.SelectedDocs}
		}
		payload = completionReq

	default: // "rag" mode
		// Use chat completions with context
		endpoint = "/v1/chat/completions"
		messages := []Message{}
		
		if reqData.SystemPrompt != "" {
			messages = append(messages, Message{Role: "system", Content: reqData.SystemPrompt})
		}
		
		// Add history
		messages = append(messages, reqData.History...)
		
		// Add current message
		messages = append(messages, Message{Role: "user", Content: reqData.Message})

		chatReq := ChatRequest{
			Model:         "private-gpt",
			Messages:      messages,
			UseContext:    reqData.Config.UseContext, // Use the config setting
			IncludeSources: true,
			Stream:        false,
			MaxTokens:     reqData.Config.MaxTokens,
			Temperature:   reqData.Config.Temperature,
		}
		
		if reqData.Config.UseContext && len(reqData.Config.SelectedDocs) > 0 {
			chatReq.ContextFilter = &ContextFilter{DocsIds: reqData.Config.SelectedDocs}
		}
		payload = chatReq
	}

	// Send request to PrivateGPT
	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshaling request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequest("POST", PRIVATEGPT_HOST+endpoint, bytes.NewReader(jsonData))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error forwarding request: %v", err)
		http.Error(w, "PrivateGPT API error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	
	log.Printf("Chat request processed - Mode: %s, Endpoint: %s", reqData.Config.Mode, endpoint)
}

// Delete all files handler
func deleteAllFilesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.Printf("Starting delete all files operation...")

	// First, get the list of all files
	resp, err := http.Get(PRIVATEGPT_HOST + "/v1/ingest/list")
	if err != nil {
		log.Printf("Error getting file list for deletion: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error": "Failed to get file list from PrivateGPT",
			"details": err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("PrivateGPT returned error status for file list: %d", resp.StatusCode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error": fmt.Sprintf("PrivateGPT error getting file list (status %d)", resp.StatusCode),
		})
		return
	}

	// Parse the file list
	var listResp ListFilesResponse
	err = json.NewDecoder(resp.Body).Decode(&listResp)
	if err != nil {
		log.Printf("Error parsing file list for deletion: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error": "Failed to parse file list",
			"details": err.Error(),
		})
		return
	}

	if len(listResp.Data) == 0 {
		log.Printf("No files to delete")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "No files to delete",
			"deleted_count": 0,
			"failed_count": 0,
		})
		return
	}

	// Delete each file
	var deletedCount, failedCount int
	var failedFiles []string
	
	log.Printf("Deleting %d files...", len(listResp.Data))

	for _, file := range listResp.Data {
		deleteReq, err := http.NewRequest("DELETE", PRIVATEGPT_HOST+"/v1/ingest/"+file.DocID, nil)
		if err != nil {
			log.Printf("Error creating delete request for %s: %v", file.DocID, err)
			failedCount++
			fileName := "Unknown"
			if file.DocMetadata != nil {
				if name, ok := file.DocMetadata["file_name"].(string); ok {
					fileName = name
				}
			}
			failedFiles = append(failedFiles, fileName)
			continue
		}

		client := &http.Client{Timeout: 30 * time.Second}
		deleteResp, err := client.Do(deleteReq)
		if err != nil {
			log.Printf("Error deleting file %s: %v", file.DocID, err)
			failedCount++
			fileName := "Unknown"
			if file.DocMetadata != nil {
				if name, ok := file.DocMetadata["file_name"].(string); ok {
					fileName = name
				}
			}
			failedFiles = append(failedFiles, fileName)
			continue
		}
		deleteResp.Body.Close()

		if deleteResp.StatusCode == 200 {
			deletedCount++
			fileName := "Unknown"
			if file.DocMetadata != nil {
				if name, ok := file.DocMetadata["file_name"].(string); ok {
					fileName = name
				}
			}
			log.Printf("Successfully deleted file: %s (%s)", fileName, file.DocID)
		} else {
			failedCount++
			fileName := "Unknown"
			if file.DocMetadata != nil {
				if name, ok := file.DocMetadata["file_name"].(string); ok {
					fileName = name
				}
			}
			failedFiles = append(failedFiles, fileName)
			log.Printf("Failed to delete file %s (%s) - status: %d", fileName, file.DocID, deleteResp.StatusCode)
		}
	}

	// Prepare response
	result := map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Bulk delete completed: %d deleted, %d failed", deletedCount, failedCount),
		"deleted_count": deletedCount,
		"failed_count": failedCount,
		"total_files": len(listResp.Data),
	}

	if len(failedFiles) > 0 {
		result["failed_files"] = failedFiles
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)

	log.Printf("Delete all files completed: %d deleted, %d failed out of %d total", 
		deletedCount, failedCount, len(listResp.Data))
}

// Processing status handler - check if specific files are still being processed
func processingStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get filename parameter
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "filename parameter is required",
		})
		return
	}

	// Check if file exists in PrivateGPT
	resp, err := http.Get(PRIVATEGPT_HOST + "/v1/ingest/list")
	if err != nil {
		log.Printf("Error checking processing status: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Failed to check PrivateGPT status",
			"filename": filename,
			"processing": false,
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "PrivateGPT API error",
			"filename": filename,
			"processing": false,
		})
		return
	}

	var listResp ListFilesResponse
	err = json.NewDecoder(resp.Body).Decode(&listResp)
	if err != nil {
		log.Printf("Error parsing file list: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Failed to parse response",
			"filename": filename,
			"processing": false,
		})
		return
	}

	// Check if the file exists
	fileExists := false
	for _, file := range listResp.Data {
		if file.DocMetadata != nil {
			if name, ok := file.DocMetadata["file_name"].(string); ok && name == filename {
				fileExists = true
				break
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"filename": filename,
		"exists": fileExists,
		"processing": !fileExists, // If file doesn't exist, assume it's still processing
		"status": map[string]interface{}{
			"completed": fileExists,
			"message": func() string {
				if fileExists {
					return "File processing completed"
				}
				return "File is still being processed"
			}(),
		},
	})
	
	log.Printf("Processing status check for %s: exists=%t", filename, fileExists)
}

// Clear history handler (client-side operation, just returns success)
func clearHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "History cleared successfully"})
}

// Embeddings handler
func embeddingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest("POST", PRIVATEGPT_HOST+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error forwarding embeddings request: %v", err)
		http.Error(w, "PrivateGPT API error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// Proxy handler for PrivateGPT API
func createProxy() *httputil.ReverseProxy {
	target, _ := url.Parse(PRIVATEGPT_HOST)
	
	proxy := httputil.NewSingleHostReverseProxy(target)
	
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		req.URL.Host = target.Host
		req.URL.Scheme = target.Scheme
		
		log.Printf("Proxying %s %s to %s", req.Method, req.URL.Path, target.String()+req.URL.Path)
	}
	
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Proxy error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "PrivateGPT API is not available",
			"message": err.Error(),
		})
	}
	
	return proxy
}

// Static file handler
func staticHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.ServeFile(w, r, "static/index.html")
		return
	}
	
	path := strings.TrimPrefix(r.URL.Path, "/")
	fullPath := filepath.Join("static", path)
	
	if strings.Contains(path, "..") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	
	http.ServeFile(w, r, fullPath)
}

func main() {
	log.Printf("Starting PrivateGPT Bridge Server on port %s", SERVER_PORT)
	log.Printf("PrivateGPT API: %s", PRIVATEGPT_HOST)

	proxy := createProxy()

	mux := http.NewServeMux()
	
	// API routes
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/api/upload", uploadHandler)
	mux.HandleFunc("/api/chat", chatHandler)
	mux.HandleFunc("/api/files", listFilesHandler)
	mux.HandleFunc("/api/files/", deleteFileHandler) // DELETE /api/files/{doc_id}
	mux.HandleFunc("/api/files/delete-all", deleteAllFilesHandler) // DELETE /api/files/delete-all
	mux.HandleFunc("/api/processing-status", processingStatusHandler) // GET /api/processing-status?filename=example.pdf
	mux.HandleFunc("/api/clear-history", clearHistoryHandler)
	mux.HandleFunc("/api/embeddings", embeddingsHandler)
	
	// PrivateGPT API proxy routes (for direct API access)
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})
	
	// Static files and UI
	mux.HandleFunc("/", staticHandler)

	handler := corsMiddleware(mux)

	if _, err := os.Stat("static"); os.IsNotExist(err) {
		log.Println("Warning: static directory not found. Creating it...")
		os.MkdirAll("static", 0755)
	}

	log.Printf("Bridge server running on http://localhost%s", SERVER_PORT)
	log.Printf("Web UI available at http://localhost%s", SERVER_PORT)
	log.Printf("API endpoints:")
	log.Printf("  GET  /health - Health check")
	log.Printf("  POST /api/upload - Upload files")
	log.Printf("  GET  /api/files - List files")
	log.Printf("  DELETE /api/files/{doc_id} - Delete file")
	log.Printf("  DELETE /api/files/delete-all - Delete all files")
	log.Printf("  GET  /api/processing-status?filename=file.pdf - Check processing status")
	log.Printf("  POST /api/chat - Chat with modes: rag, search, basic, summarize")
	log.Printf("  POST /api/clear-history - Clear chat history")
	log.Printf("  POST /api/embeddings - Generate embeddings")
	log.Fatal(http.ListenAndServe(SERVER_PORT, handler))
}
