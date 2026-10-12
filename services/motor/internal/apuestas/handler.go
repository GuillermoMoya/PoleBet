package apuestas

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

// Register agrega POST /apuestas al mux.
//
// proteger envuelve el handler con la autenticacion (en main:
// autenticador.ExigirRol(auth.RolJugador, h)) y usuarioDe saca el id del
// usuario de la sesion (auth.SesionDe). Se reciben como funciones para que
// este paquete no dependa de auth y se pueda probar solo.
func Register(
	mux *http.ServeMux,
	svc *Servicio,
	proteger func(http.Handler) http.Handler,
	usuarioDe func(*http.Request) (string, bool),
) {
	mux.Handle("POST /apuestas", proteger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := usuarioDe(r)
		if !ok {
			escribir(w, http.StatusUnauthorized, map[string]string{"error": "no autorizado"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var p Peticion
		if err := dec.Decode(&p); err != nil {
			escribir(w, http.StatusBadRequest, map[string]string{"error": "cuerpo invalido"})
			return
		}

		apuesta, err := svc.Apostar(r.Context(), userID, p, r.Header.Get("Idempotency-Key"))
		if err != nil {
			status, msg := traducir(err)
			escribir(w, status, map[string]string{"error": msg})
			return
		}
		escribir(w, http.StatusCreated, apuesta)
	})))
}

// traducir convierte un error de negocio en codigo HTTP y mensaje para el jugador.
func traducir(err error) (int, string) {
	switch {
	case errors.Is(err, ErrPeticionInvalida):
		return http.StatusBadRequest, "Revisa mercado, opcion y monto (el monto debe ser mayor que 0)"
	case errors.Is(err, ErrMercadoNoExiste):
		return http.StatusNotFound, "El mercado no existe"
	case errors.Is(err, ErrMercadoCerrado):
		return http.StatusConflict, "Mercado cerrado"
	case errors.Is(err, ErrOpcionInvalida):
		return http.StatusBadRequest, "La opcion no pertenece al mercado"
	case errors.Is(err, ErrSaldoInsuficiente):
		return http.StatusUnprocessableEntity, "Saldo insuficiente"
	case errors.Is(err, ErrUsuarioNoExiste):
		return http.StatusUnprocessableEntity, "El usuario no existe"
	case errors.Is(err, ErrPagosNoDisponible):
		return http.StatusBadGateway, "Pagos no esta disponible, intenta de nuevo"
	case errors.Is(err, ErrPagosConflicto):
		return http.StatusBadGateway, "Pagos detectó un conflicto de idempotencia"
	default:
		log.Printf("error inesperado al apostar: %v", err)
		return http.StatusInternalServerError, "Error interno"
	}
}

func escribir(w http.ResponseWriter, status int, cuerpo any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(cuerpo)
}
