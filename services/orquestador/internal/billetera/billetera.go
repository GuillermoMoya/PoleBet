// Package billetera implementa el flujo de compra de tokens (HU2-04, RF-05,
// seccion 3.3 de documentation/orquestador-api.md):
//
//	GET  /billetera          saldo y tasa      (el Gateway la expone como /api/...)
//	POST /billetera/compras  compra de tokens  (Idempotency-Key obligatoria)
//
// Solo jugadores (sesion.ExigirJugador). Las reglas del monto (mayor que 0,
// multiplo de la tasa, tope) las decide Pagos; aqui solo se valida la forma
// del cuerpo y se traduce la respuesta de Pagos a la API publica.
package billetera

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

// LargoMaximoClave es el largo maximo de la Idempotency-Key del front.
const LargoMaximoClave = 128

// Pagos es lo que este flujo necesita del cliente interno (interno.Cliente lo
// cumple). Es una interfaz para poder probar el flujo sin red.
type Pagos interface {
	Hacer(ctx context.Context, p interno.Peticion) (interno.Respuesta, error)
}

// Billetera es la respuesta publica de GET /billetera.
type Billetera struct {
	Saldo       int64 `json:"saldo"`
	CopPorToken int64 `json:"cop_por_token"`
}

// Compra es la respuesta publica de POST /billetera/compras. Pagos ya responde
// con estos mismos campos.
type Compra struct {
	CompraID          int64  `json:"compra_id"`
	TokensAcreditados int64  `json:"tokens_acreditados"`
	Saldo             int64  `json:"saldo"`
	CreadaEn          string `json:"creada_en"`
}

// walletPagos es la respuesta de GET /internal/wallets/{user_id}.
type walletPagos struct {
	Balance     int64 `json:"balance"`
	CopPorToken int64 `json:"cop_por_token"`
}

// ClaveIdempotencia es la clave que valida Pagos: purchase:{usuario}:{clave del front}.
// Lleva el user_id para que dos jugadores con la misma clave no choquen.
func ClaveIdempotencia(userID, clave string) string {
	return "purchase:" + userID + ":" + clave
}

// Register agrega las rutas, protegidas con el middleware de sesion.
func Register(mux *http.ServeMux, pagos Pagos) {
	mux.Handle("GET /billetera", sesion.ExigirJugador(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			userID, _ := sesion.UsuarioDe(r.Context())
			b, err := Ver(r.Context(), pagos, userID)
			if err != nil {
				errores.Responder(w, err)
				return
			}
			httpx.EscribirJSON(w, http.StatusOK, b)
		},
	)))

	mux.Handle("POST /billetera/compras", sesion.ExigirJugador(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			userID, _ := sesion.UsuarioDe(r.Context())
			clave := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if clave == "" || len(clave) > LargoMaximoClave {
				errores.Responder(w, errores.FaltaIdempotencyKey)
				return
			}
			var cuerpo struct {
				MontoCOP *int64 `json:"monto_cop"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&cuerpo); err != nil || cuerpo.MontoCOP == nil {
				errores.Responder(w, errores.CuerpoInvalido)
				return
			}
			c, estado, err := Comprar(r.Context(), pagos, userID, *cuerpo.MontoCOP, clave)
			if err != nil {
				errores.Responder(w, err)
				return
			}
			httpx.EscribirJSON(w, estado, c)
		},
	)))
}

// Ver trae el saldo y la tasa de Pagos.
func Ver(ctx context.Context, pagos Pagos, userID string) (Billetera, error) {
	resp, err := pagos.Hacer(ctx, interno.Peticion{Metodo: http.MethodGet, Ruta: "/internal/wallets/" + userID})
	if err != nil {
		return Billetera{}, err // ErrNoDisponible -> 503; el resto -> 500
	}
	if resp.Estado != http.StatusOK {
		// 404 (sin billetera) no deberia pasar: el registro siempre la crea.
		return Billetera{}, fmt.Errorf("pagos respondio %d al leer la billetera: %s", resp.Estado, resp.Cuerpo)
	}
	var w walletPagos
	if err := json.Unmarshal(resp.Cuerpo, &w); err != nil {
		return Billetera{}, fmt.Errorf("respuesta invalida de Pagos: %w", err)
	}
	return Billetera{Saldo: w.Balance, CopPorToken: w.CopPorToken}, nil
}

// Comprar pide la compra a Pagos y traduce el resultado. Devuelve el codigo
// HTTP publico: 201 si la compra es nueva, 200 si era un reenvio.
func Comprar(ctx context.Context, pagos Pagos, userID string, montoCOP int64, clave string) (Compra, int, error) {
	resp, err := pagos.Hacer(ctx, interno.Peticion{
		Metodo:         http.MethodPost,
		Ruta:           "/internal/purchases",
		Cuerpo:         map[string]any{"user_id": userID, "monto_cop": montoCOP},
		IdempotencyKey: ClaveIdempotencia(userID, clave),
	})
	if err != nil {
		return Compra{}, 0, err
	}

	switch resp.Estado {
	case http.StatusCreated, http.StatusOK:
		var c Compra
		if err := json.Unmarshal(resp.Cuerpo, &c); err != nil {
			return Compra{}, 0, fmt.Errorf("respuesta invalida de Pagos: %w", err)
		}
		return c, resp.Estado, nil
	case http.StatusConflict:
		return Compra{}, 0, errores.ClaveReutilizada
	case http.StatusUnprocessableEntity:
		return Compra{}, 0, errores.MontoInvalido
	default:
		// 400 (clave mal armada), 401 (token) o 404 (sin billetera) son errores
		// nuestros, no del jugador.
		return Compra{}, 0, fmt.Errorf("pagos respondio %d al comprar: %s", resp.Estado, resp.Cuerpo)
	}
}
