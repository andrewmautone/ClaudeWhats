package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrNoKey = errors.New("GEMINI_API_KEY não configurada")

type Client struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

func New(apiKey, model string) *Client {
	return &Client{APIKey: apiKey, Model: model, BaseURL: "https://generativelanguage.googleapis.com", HTTP: &http.Client{Timeout: 120 * time.Second}}
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inline_data,omitempty"`
}
type inlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}
type request struct {
	Contents []struct {
		Parts []part `json:"parts"`
	} `json:"contents"`
}

func (c *Client) generate(ctx context.Context, parts []part) (string, error) {
	if c.APIKey == "" {
		return "", ErrNoKey
	}
	var req request
	req.Contents = append(req.Contents, struct {
		Parts []part `json:"parts"`
	}{parts})
	body, _ := json.Marshal(req)
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", c.BaseURL, c.Model, c.APIKey)
	hr, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hr.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("gemini http %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", err
	}
	if len(out.Candidates) == 0 {
		return "", errors.New("gemini: resposta sem candidatos")
	}
	var sb strings.Builder
	for _, p := range out.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	return strings.TrimSpace(sb.String()), nil
}

func (c *Client) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	return c.generate(ctx, []part{
		{Text: "Transcreva este áudio fielmente, em português do Brasil quando for o idioma falado. Responda só com a transcrição, sem comentários."},
		{InlineData: &inlineData{MimeType: mime, Data: base64.StdEncoding.EncodeToString(audio)}},
	})
}

func (c *Client) Describe(ctx context.Context, image []byte, mime, caption string) (string, error) {
	prompt := "Descreva esta imagem em uma ou duas frases em português e transcreva qualquer texto visível. Responda só com a descrição."
	if caption != "" {
		prompt += " Legenda enviada junto: " + caption
	}
	return c.generate(ctx, []part{
		{Text: prompt},
		{InlineData: &inlineData{MimeType: mime, Data: base64.StdEncoding.EncodeToString(image)}},
	})
}

func (c *Client) Summarize(ctx context.Context, instructions, text string) (string, error) {
	return c.generate(ctx, []part{{Text: instructions + "\n\n---\n\n" + text}})
}
