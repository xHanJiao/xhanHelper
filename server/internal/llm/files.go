package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// FilesClient — DeepSeek Files API 客户端。
//
// 只做上传：官方没有下载接口，也没有删除接口，文件归属于 API key。
// 因此 file_id 只能在本服务端（与 LLM 调用同一把 key）创建，且本地必须留副本。
type FilesClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewFilesClient 创建 Files API 客户端
func NewFilesClient(apiKey, baseURL string) *FilesClient {
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	return &FilesClient{
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

type fileUploadResponse struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Bytes    int64  `json:"bytes"`
	Filename string `json:"filename"`
	Purpose  string `json:"purpose"`
	Error    *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Upload 以 purpose=user_data 上传一个文件，返回 file_id。
// 不传 expires_after（= 永久有效），避免过期后历史消息的图再也送不进模型。
func (c *FilesClient) Upload(ctx context.Context, filename string, data []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("files: create form file: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return "", fmt.Errorf("files: write form data: %w", err)
	}
	if err := w.WriteField("purpose", "user_data"); err != nil {
		return "", fmt.Errorf("files: write purpose: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("files: close writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/files", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("files: upload: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("files: read response: %w", err)
	}

	var out fileUploadResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("files: decode response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(body)
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return "", fmt.Errorf("files: upload failed (status %d): %s", resp.StatusCode, msg)
	}
	if out.ID == "" {
		return "", fmt.Errorf("files: empty file id in response")
	}
	return out.ID, nil
}
