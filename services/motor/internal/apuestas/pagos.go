package apuestas

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rutas internas REST acordadas para Motor → Pagos.
const RutaDebito = "/internal/debit"
const RutaLiquidacion = "/internal/settlements"

// PagosHTTP llama al servicio Pagos por REST.
//
// Codigos de Pagos:
//
//	200/201 -> debitado
//	409     -> clave reutilizada con contenido distinto
//	422     -> saldo insuficiente
//	404     -> el usuario no existe
//	otro    -> Pagos no esta disponible
type PagosHTTP struct {
	BaseURL string
	Token   string
	Cliente *http.Client
}

func NuevoPagosHTTP(baseURL, token string) *PagosHTTP {
	return &PagosHTTP{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Cliente: &http.Client{Timeout: 5 * time.Second},
	}
}

func (p *PagosHTTP) Debitar(ctx context.Context, d Debito) error {
	cuerpo, err := json.Marshal(d)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+RutaDebito, bytes.NewReader(cuerpo))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", d.IdempotencyKey)
	req.Header.Set("X-Internal-Service-Token", p.Token)

	resp, err := p.Cliente.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPagosNoDisponible, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusConflict:
		return ErrPagosConflicto
	case http.StatusUnprocessableEntity:
		return ErrSaldoInsuficiente
	case http.StatusNotFound:
		return ErrUsuarioNoExiste
	default:
		return fmt.Errorf("%w: respondio %d", ErrPagosNoDisponible, resp.StatusCode)
	}
}

type Liquidacion struct {
	BetID          string  `json:"bet_id"`
	Resultado      string  `json:"result"`
	Puntos         float64 `json:"points"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type ResultadoLiquidacion struct {
	BetID           string  `json:"bet_id"`
	Estado          string  `json:"status"`
	PuntosOtorgados float64 `json:"points_awarded"`
	PuntosTotales   float64 `json:"points_total"`
	Nueva           bool    `json:"nueva"`
}

func (p *PagosHTTP) Liquidar(ctx context.Context, l Liquidacion) (ResultadoLiquidacion, error) {
	cuerpo, err := json.Marshal(l)
	if err != nil {
		return ResultadoLiquidacion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+RutaLiquidacion, bytes.NewReader(cuerpo))
	if err != nil {
		return ResultadoLiquidacion{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", l.IdempotencyKey)
	req.Header.Set("X-Internal-Service-Token", p.Token)

	resp, err := p.Cliente.Do(req)
	if err != nil {
		return ResultadoLiquidacion{}, fmt.Errorf("%w: %v", ErrPagosNoDisponible, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
		if resp.StatusCode == http.StatusConflict {
			return ResultadoLiquidacion{}, ErrPagosConflicto
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return ResultadoLiquidacion{}, fmt.Errorf("%w: token interno rechazado (401)", ErrPagosNoDisponible)
		case http.StatusNotFound:
			return ResultadoLiquidacion{}, fmt.Errorf("%w: apuesta no existe en Pagos (404)", ErrPagosNoDisponible)
		case http.StatusUnprocessableEntity:
			return ResultadoLiquidacion{}, fmt.Errorf("%w: contrato o puntos rechazados por Pagos (422)", ErrPagosNoDisponible)
		}
		return ResultadoLiquidacion{}, fmt.Errorf("%w: respondio %d", ErrPagosNoDisponible, resp.StatusCode)
	}

	var resultado ResultadoLiquidacion
	dec := json.NewDecoder(io.LimitReader(resp.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&resultado); err != nil {
		return ResultadoLiquidacion{}, fmt.Errorf("%w: respuesta invalida", ErrPagosNoDisponible)
	}
	if resultado.BetID != l.BetID ||
		resultado.Estado != l.Resultado ||
		resultado.PuntosOtorgados != l.Puntos {
		return ResultadoLiquidacion{}, fmt.Errorf("%w: respuesta incompatible", ErrPagosNoDisponible)
	}
	return resultado, nil
}

// PagosMock simula a Pagos mientras el endpoint real no exista. Cada usuario
// empieza con SaldoInicial tokens, el saldo nunca baja de 0 y repetir una
// clave de idempotencia no debita dos veces. Se activa con PAGOS_MOCK=true.
type PagosMock struct {
	SaldoInicial int64

	mu      sync.Mutex
	saldos  map[string]int64
	hechas  map[string]bool
	Debitos []Debito // historial, util para pruebas
}

func NuevoPagosMock(saldoInicial int64) *PagosMock {
	return &PagosMock{
		SaldoInicial: saldoInicial,
		saldos:       map[string]int64{},
		hechas:       map[string]bool{},
	}
}

func (m *PagosMock) Debitar(_ context.Context, d Debito) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hechas[d.IdempotencyKey] {
		return nil
	}
	saldo, existe := m.saldos[d.UserID]
	if !existe {
		saldo = m.SaldoInicial
	}
	if saldo < d.Amount {
		return ErrSaldoInsuficiente
	}
	m.saldos[d.UserID] = saldo - d.Amount
	m.hechas[d.IdempotencyKey] = true
	m.Debitos = append(m.Debitos, d)
	return nil
}

// Saldo devuelve el saldo simulado de un usuario.
func (m *PagosMock) Saldo(userID string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.saldos[userID]; ok {
		return s
	}
	return m.SaldoInicial
}
