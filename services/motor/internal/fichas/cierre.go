package fichas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HU2-12: cierre del torneo. Al terminar la carrera (y despues de liquidar las
// estaticas), el Motor envia a Pagos las fichas finales de cada inscrito. Pagos,
// dueño del dinero, calcula y paga el reparto del botin en una sola
// transaccion y guarda el resultado en event_entries. Solo cuando Pagos
// confirma, el Motor borra el estado del torneo en Redis (RF-16).
//
// Orden y reintentos: si Pagos falla, Redis NO se limpia y el cierre se puede
// repetir; Pagos es idempotente (misma clave event-close:{evento}). Si Pagos
// confirmo pero la limpieza fallo, repetir el cierre devuelve el reparto ya
// guardado y vuelve a intentar limpiar. Si Redis ya estaba limpio, se envia la
// lista vacia y Pagos devuelve el reparto guardado.

// RutaCierre es la ruta interna de Pagos para cerrar el evento (HU2-12).
const RutaCierre = "/internal/events/%s/close"

var (
	// ErrCierreRechazado: Pagos rechazo el cierre (evento sin largar, fichas
	// que no coinciden con los inscritos o un reparto previo distinto).
	ErrCierreRechazado = errors.New("pagos rechazo el cierre del evento")
	// ErrPagosNoDisponible: Pagos no respondio o respondio un error inesperado.
	ErrPagosNoDisponible = errors.New("pagos no esta disponible")
)

// FichasFinales es lo que el Motor reporta de cada inscrito al cerrar.
type FichasFinales struct {
	UserID       string `json:"user_id"`
	ChipsFinal   int64  `json:"chips_final"`
	ChipsWagered int64  `json:"chips_wagered"`
}

// PremioJugador es el resultado del reparto para un inscrito.
type PremioJugador struct {
	UserID        string `json:"user_id"`
	PositionFinal int    `json:"position_final"`
	ChipsFinal    int64  `json:"chips_final"`
	ChipsWagered  int64  `json:"chips_wagered"`
	Eligible      bool   `json:"eligible"`
	Prize         int64  `json:"prize"`
}

// ResultadoCierre es la respuesta de Pagos al cerrar el evento.
type ResultadoCierre struct {
	EventID          string          `json:"event_id"`
	Status           string          `json:"status"`
	TournamentStatus string          `json:"tournament_status"`
	Pot              int64           `json:"pot"`
	Commission       int64           `json:"commission"`
	PrizesTotal      int64           `json:"prizes_total"`
	Residue          int64           `json:"residue"`
	Players          []PremioJugador `json:"players"`
	Nueva            bool            `json:"nueva"`
}

// PagosCierre es lo que el cierre necesita de Pagos (un doble en las pruebas).
type PagosCierre interface {
	CerrarEvento(ctx context.Context, eventoID string, jugadores []FichasFinales) (ResultadoCierre, error)
}

// CerrarTorneo reporta las fichas finales a Pagos y, si Pagos confirma el
// reparto, limpia Redis. Es seguro repetirlo (ver comentario de arriba).
func (a *Almacen) CerrarTorneo(ctx context.Context, eventoID string, pagos PagosCierre) (ResultadoCierre, error) {
	if eventoID == "" || pagos == nil {
		return ResultadoCierre{}, ErrPeticionInvalida
	}
	estados, err := a.store.Estados(ctx, eventoID)
	if err != nil {
		return ResultadoCierre{}, fmt.Errorf("leer fichas finales: %w", err)
	}
	jugadores := make([]FichasFinales, 0, len(estados))
	for _, e := range estados {
		jugadores = append(jugadores, FichasFinales{UserID: e.UsuarioID, ChipsFinal: e.Fichas, ChipsWagered: e.Apostado})
	}

	res, err := pagos.CerrarEvento(ctx, eventoID, jugadores)
	if err != nil {
		// Redis queda intacto para poder reintentar el cierre.
		return ResultadoCierre{}, err
	}
	if err := a.store.Limpiar(ctx, eventoID); err != nil {
		// El reparto ya quedo en Pagos; solo falta limpiar. Repetir el cierre
		// es seguro: Pagos devuelve lo guardado y se vuelve a limpiar.
		return res, fmt.Errorf("reparto guardado en Pagos pero no se pudo limpiar Redis: %w", err)
	}
	return res, nil
}

// PagosCierreHTTP llama a POST /internal/events/{id}/close de Pagos.
type PagosCierreHTTP struct {
	BaseURL string
	Token   string
	Cliente *http.Client
}

func NuevoPagosCierreHTTP(baseURL, token string) *PagosCierreHTTP {
	return &PagosCierreHTTP{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Cliente: &http.Client{Timeout: 10 * time.Second},
	}
}

var _ PagosCierre = (*PagosCierreHTTP)(nil)

func (p *PagosCierreHTTP) CerrarEvento(ctx context.Context, eventoID string, jugadores []FichasFinales) (ResultadoCierre, error) {
	cuerpo, err := json.Marshal(map[string]any{"players": jugadores})
	if err != nil {
		return ResultadoCierre{}, err
	}
	url := p.BaseURL + fmt.Sprintf(RutaCierre, eventoID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(cuerpo))
	if err != nil {
		return ResultadoCierre{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "event-close:"+eventoID)
	req.Header.Set("X-Internal-Service-Token", p.Token)

	resp, err := p.Cliente.Do(req)
	if err != nil {
		return ResultadoCierre{}, fmt.Errorf("%w: %v", ErrPagosNoDisponible, err)
	}
	defer resp.Body.Close()
	cuerpoResp := io.LimitReader(resp.Body, 1<<20)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var r ResultadoCierre
		if err := json.NewDecoder(cuerpoResp).Decode(&r); err != nil {
			return ResultadoCierre{}, fmt.Errorf("%w: respuesta invalida", ErrPagosNoDisponible)
		}
		// Integridad (RNF-11): premios + comision + residuo deben sumar el botin.
		if r.PrizesTotal+r.Commission+r.Residue != r.Pot {
			return ResultadoCierre{}, fmt.Errorf("%w: el reparto no cuadra con el botin", ErrPagosNoDisponible)
		}
		return r, nil
	case http.StatusConflict, http.StatusNotFound, http.StatusBadRequest:
		detalle, _ := io.ReadAll(io.LimitReader(cuerpoResp, 2048))
		return ResultadoCierre{}, fmt.Errorf("%w (%d): %s", ErrCierreRechazado, resp.StatusCode, strings.TrimSpace(string(detalle)))
	default:
		_, _ = io.Copy(io.Discard, cuerpoResp)
		return ResultadoCierre{}, fmt.Errorf("%w: respondio %d", ErrPagosNoDisponible, resp.StatusCode)
	}
}
