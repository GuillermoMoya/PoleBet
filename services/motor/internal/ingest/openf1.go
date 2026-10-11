package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// baseURLConst es variable (no const) para que los tests puedan apuntarla a
// un httptest.Server.
var baseURLConst = "https://api.openf1.org/v1"

// OpenF1Client es un cliente HTTP mínimo para la API de OpenF1.
type OpenF1Client struct {
	http *http.Client
}

func NuevoOpenF1Client() *OpenF1Client {
	return &OpenF1Client{
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *OpenF1Client) Posiciones(ctx context.Context, sessionKey int) ([]PosicionOpenF1, error) {
	var out []PosicionOpenF1
	if err := c.get(ctx, "/position", map[string]string{
		"session_key": fmt.Sprintf("%d", sessionKey),
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *OpenF1Client) Pilotos(ctx context.Context, sessionKey int) ([]PilotoOpenF1, error) {
	var out []PilotoOpenF1
	if err := c.get(ctx, "/drivers", map[string]string{
		"session_key": fmt.Sprintf("%d", sessionKey),
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *OpenF1Client) Sesiones(ctx context.Context, year int) ([]SesionOpenF1, error) {
	var out []SesionOpenF1
	if err := c.get(ctx, "/sessions", map[string]string{
		"year": fmt.Sprintf("%d", year),
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *OpenF1Client) get(ctx context.Context, path string, query map[string]string, out any) error {
	u, err := url.Parse(baseURLConst + path)
	if err != nil {
		return err
	}
	q := u.Query()
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openf1 %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("openf1 %s: status %d: %s", path, resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
