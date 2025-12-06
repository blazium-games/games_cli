package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

// Client wraps HTTP client with authentication
type Client struct {
	baseURL     string
	accessToken string
	secretKey   string
	httpClient  *http.Client
}

// NewClient creates a new authenticated HTTP client
func NewClient(baseURL, accessToken, secretKey string) *Client {
	return &Client{
		baseURL:     baseURL,
		accessToken: accessToken,
		secretKey:   secretKey,
		httpClient:  &http.Client{},
	}
}

// APIResponse represents the standard API response structure
type APIResponse struct {
	Success bool                   `json:"success"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// PostJSON sends a POST request with JSON body
func (c *Client) PostJSON(endpoint string, body interface{}) (*APIResponse, error) {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	url := c.baseURL + endpoint
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Access-Token", c.accessToken)
	req.Header.Set("X-Secret-Key", c.secretKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		if apiResp.Error != nil {
			return nil, fmt.Errorf("API error [%d]: %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return &apiResp, nil
}

// PostMultipart sends a POST request with multipart form data
func (c *Client) PostMultipart(endpoint string, formData map[string]string, files map[string]string, additionalHeaders map[string]string) (*APIResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add form fields
	for key, value := range formData {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("failed to write field %s: %w", key, err)
		}
	}

	// Add files
	for fieldName, filePath := range files {
		file, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
		}

		part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to create form file %s: %w", fieldName, err)
		}

		if _, err := io.Copy(part, file); err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to copy file %s: %w", filePath, err)
		}
		file.Close()
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := c.baseURL + endpoint
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Access-Token", c.accessToken)
	req.Header.Set("X-Secret-Key", c.secretKey)

	// Add additional headers
	for key, value := range additionalHeaders {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		if apiResp.Error != nil {
			return nil, fmt.Errorf("API error [%d]: %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return &apiResp, nil
}

// PostMultipartMultipleFiles sends a POST request with multiple files using the same field name
func (c *Client) PostMultipartMultipleFiles(endpoint string, formData map[string]string, fieldName string, filePaths []string, additionalHeaders map[string]string) (*APIResponse, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add form fields
	for key, value := range formData {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("failed to write field %s: %w", key, err)
		}
	}

	// Add all files with the same field name
	for _, filePath := range filePaths {
		file, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
		}

		part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to create form file: %w", err)
		}

		if _, err := io.Copy(part, file); err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to copy file %s: %w", filePath, err)
		}
		file.Close()
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := c.baseURL + endpoint
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Access-Token", c.accessToken)
	req.Header.Set("X-Secret-Key", c.secretKey)

	// Add additional headers
	for key, value := range additionalHeaders {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		if apiResp.Error != nil {
			return nil, fmt.Errorf("API error [%d]: %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return &apiResp, nil
}

// PostMultipartResume sends a POST request with multipart form data and resume support
func (c *Client) PostMultipartResume(endpoint string, formData map[string]string, filePath string, startByte, totalSize int64, sessionID string) (*APIResponse, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Seek to start position
	if startByte > 0 {
		if _, err := file.Seek(startByte, 0); err != nil {
			return nil, fmt.Errorf("failed to seek file: %w", err)
		}
	}

	// Read the chunk to upload
	remainingSize := totalSize - startByte
	chunk := make([]byte, remainingSize)
	n, err := file.Read(chunk)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	chunk = chunk[:n]

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add form fields
	for key, value := range formData {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("failed to write field %s: %w", key, err)
		}
	}

	// Add file chunk
	fieldName := "file"
	fileName := filepath.Base(filePath)
	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := part.Write(chunk); err != nil {
		return nil, fmt.Errorf("failed to write file chunk: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := c.baseURL + endpoint
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Access-Token", c.accessToken)
	req.Header.Set("X-Secret-Key", c.secretKey)
	req.Header.Set("X-Upload-Session-ID", sessionID)

	// Add Content-Range header for resume
	if startByte > 0 {
		endByte := startByte + int64(len(chunk)) - 1
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", startByte, endByte, totalSize))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Handle 206 Partial Content (resume in progress)
	if resp.StatusCode == 206 {
		return &APIResponse{Success: true, Data: map[string]interface{}{"partial": true}}, nil
	}

	var apiResp APIResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		if apiResp.Error != nil {
			return nil, fmt.Errorf("API error [%d]: %s", apiResp.Error.Code, apiResp.Error.Message)
		}
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return &apiResp, nil
}
