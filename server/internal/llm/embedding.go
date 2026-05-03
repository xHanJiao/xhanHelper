package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// Embedder — embedding API 调用器
type Embedder struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewEmbedder 创建豆包 embedding 客户端
func NewEmbedder(apiKey, baseURL, model string) *Embedder {
	if baseURL == "" {
		baseURL = "https://ark.cn-beijing.volces.com"
	}
	if model == "" {
		model = "doubao-embedding"
	}
	return &Embedder{
		apiKey:     apiKey,
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{},
	}
}

// Embed 将文本转为向量
func (e *Embedder) Embed(ctx context.Context, text string) ([]float64, error) {
	type textInput struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	body := map[string]interface{}{
		"model": e.model,
		"input": []textInput{{Type: "text", Text: text}},
	}
	b, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST",
		e.baseURL+"/api/v3/embeddings/multimodal", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	log.Printf("[embed] request: url=%s model=%s key=%s... text=%s",
		e.baseURL+"/api/v3/embeddings/multimodal", e.model,
		e.apiKey[:min(8, len(e.apiKey))],
		text[:min(80, len(text))])

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embedding status %d: %s", resp.StatusCode, string(respBody[:min(300, len(respBody))]))
	}

	var result struct {
		Data struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}

	log.Printf("[embed] dim=%d model=%s text=%s", len(result.Data.Embedding), e.model,
		text[:min(80, len(text))])
	return result.Data.Embedding, nil
}

// EmbeddingToString 向量序列化为 JSON 字符串（用于 DB 存储）
func EmbeddingToString(vec []float64) string {
	b, _ := json.Marshal(vec)
	return string(b)
}

// StringToEmbedding JSON 字符串反序列化为向量
func StringToEmbedding(s string) ([]float64, error) {
	var vec []float64
	err := json.Unmarshal([]byte(s), &vec)
	return vec, err
}
