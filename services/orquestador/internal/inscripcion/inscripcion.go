// Package inscripcion implementa el flujo de inscripcion al torneo (HU2-06,
// seccion 3.5 de documentation/orquestador-api.md):
//
//	POST /eventos/{evento_id}/inscripcion   (el Gateway la expone como /api/...)
//
// Solo jugadores (sesion.ExigirJugador). Sin cuerpo y sin Idempotency-Key del
// front: la clave la arma el orquestador y es la misma en cada intento, asi
// que un reintento o un segundo clic no cobran dos veces.
//
// Un solo paso: Pagos cobra el buy_in, lo suma al botin y crea la fila en
// event_entries en una transaccion. No llama al Motor: las fichas en Redis se
// crean en la largada. Las reglas de dinero (saldo, inscripciones abiertas)
// las decide Pagos; aqui solo se traduce su respuesta a la API publica.
package inscripcion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/arquisoft/orquestador/internal/errores"
	"github.com/arquisoft/orquestador/internal/httpx"
	"github.com/arquisoft/orquestador/internal/interno"
	"github.com/arquisoft/orquestador/internal/sesion"
)

// Pagos es lo que este flujo necesita del cliente interno (interno.Cliente lo
// cumple). Es una interfaz para poder probar el flujo sin red.
type Pagos interface {
	Hacer(ctx context.Context, p interno.Peticion) (interno.Respuesta, error)
}

// Respuesta es el cuerpo publico (201 inscrito ahora, 200 ya estaba inscrito).
type Respuesta struct {
	EventoID         string `json:"evento_id"`
	YaInscrito       bool   `json:"ya_inscrito"`
	CostoInscripcion int64  `json:"costo_inscripcion"`
	FichasIniciales  int64  `json:"fichas_iniciales"`
	Botin            int64  `json:"botin"`
	Saldo            *int64 `json:"saldo"`
}

// inscripcionPagos es la respuesta de POST /internal/events/{id}/registrations.
type inscripcionPagos struct {
	EventID      string `json:"event_id"`
	BuyIn        int64  `json:"buy_in"`
	InitialChips int64  `json:"initial_chips"`
	Pot          int64  `json:"pot"`
	Saldo        *int64 `json:"saldo"`
	Nueva        bool   `json:"nueva"`
}

// ClaveIdempotencia es la clave que valida Pagos: event-entry:{evento}:{usuario}.
func ClaveIdempotencia(eventoID, userID string) string {
	return "event-entry:" + eventoID + ":" + userID
}

// Register agrega la ruta, protegida con el middleware de sesion.
func Register(mux *http.ServeMux, pagos Pagos) {
	mux.Handle("POST /eventos/{evento_id}/inscripcion", sesion.ExigirJugador(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			userID, _ := sesion.UsuarioDe(r.Context())
			resp, estado, err := Inscribir(r.Context(), pagos, r.PathValue("evento_id"), userID)
			if err != nil {
				errores.Responder(w, err)
				return
			}
			httpx.EscribirJSON(w, estado, resp)
		},
	)))
}

// Inscribir llama a Pagos y traduce el resultado. Devuelve el codigo HTTP
// publico: 201 si se inscribio ahora, 200 si ya estaba inscrito.
func Inscribir(ctx context.Context, pagos Pagos, eventoID, userID string) (Respuesta, int, error) {
	eventoID = strings.ToLower(strings.TrimSpace(eventoID))
	if !esUUID(eventoID) {
		// Un id con otra forma no puede existir; se responde igual que uno inexistente.
		return Respuesta{}, 0, errores.EventoNoExiste
	}

	resp, err := pagos.Hacer(ctx, interno.Peticion{
		Metodo:         http.MethodPost,
		Ruta:           "/internal/events/" + eventoID + "/registrations",
		Cuerpo:         map[string]string{"user_id": userID},
		IdempotencyKey: ClaveIdempotencia(eventoID, userID),
	})
	if err != nil {
		return Respuesta{}, 0, err // ErrNoDisponible -> 503; el resto -> 500
	}

	switch resp.Estado {
	case http.StatusCreated, http.StatusOK:
		var p inscripcionPagos
		if err := json.Unmarshal(resp.Cuerpo, &p); err != nil {
			return Respuesta{}, 0, fmt.Errorf("respuesta invalida de Pagos: %w", err)
		}
		estado := http.StatusOK
		if p.Nueva {
			estado = http.StatusCreated
		}
		return Respuesta{
			EventoID:         eventoID,
			YaInscrito:       !p.Nueva,
			CostoInscripcion: p.BuyIn,
			FichasIniciales:  p.InitialChips,
			Botin:            p.Pot,
			Saldo:            p.Saldo,
		}, estado, nil
	case http.StatusNotFound:
		return Respuesta{}, 0, errores.EventoNoExiste
	case http.StatusConflict:
		return Respuesta{}, 0, errores.InscripcionesCerradas
	case http.StatusUnprocessableEntity:
		return Respuesta{}, 0, errores.SaldoInsuficiente
	default:
		// 400 (clave mal armada) o 401 (token) son errores nuestros, no del jugador.
		return Respuesta{}, 0, fmt.Errorf("pagos respondio %d al inscribir: %s", resp.Estado, resp.Cuerpo)
	}
}

// esUUID revisa el formato 8-4-4-4-12 en hexadecimal (minusculas).
func esUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
				return false
			}
		}
	}
	return true
}
