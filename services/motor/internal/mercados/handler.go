package mercados

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/arquisoft/motor/internal/httpx"
)

// Register agrega las rutas de HU-11:
//
//	POST /mercados         abre un mercado (solo administrador: 401 / 403)
//	GET  /mercados/actual  el mercado mas reciente, o null (cualquier sesion)
//
// soloAdmin y conSesion envuelven los handlers con la autenticacion (en main:
// a.ExigirRol(auth.RolAdmin, h) y a.ExigirSesion(h)); usuarioDe saca el id de
// la sesion. Se reciben como funciones para que este paquete no dependa de
// auth y se pueda probar solo.
func Register(
	mux *http.ServeMux,
	svc *Servicio,
	soloAdmin, conSesion func(http.Handler) http.Handler,
	usuarioDe func(*http.Request) (string, bool),
) {
	mux.Handle("POST /mercados", soloAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adminID, ok := usuarioDe(r)
		if !ok {
			httpx.EscribirJSON(w, http.StatusUnauthorized, map[string]string{"error": "no autorizado"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var p Peticion
		if err := dec.Decode(&p); err != nil {
			httpx.EscribirJSON(w, http.StatusBadRequest, map[string]string{"error": "cuerpo invalido"})
			return
		}

		vista, err := svc.Abrir(r.Context(), adminID, p)
		if err != nil {
			status, msg := traducir(err)
			httpx.EscribirJSON(w, status, map[string]string{"error": msg})
			return
		}
		httpx.EscribirJSON(w, http.StatusCreated, vista)
	})))

	mux.Handle("GET /mercados/actual", conSesion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vista, err := svc.Actual(r.Context())
		if errors.Is(err, ErrNoHayMercado) {
			httpx.EscribirJSON(w, http.StatusOK, nil) // "null": aun no se ha abierto ninguno
			return
		}
		if err != nil {
			status, msg := traducir(err)
			httpx.EscribirJSON(w, status, map[string]string{"error": msg})
			return
		}
		httpx.EscribirJSON(w, http.StatusOK, vista)
	})))
}

// traducir convierte un error de negocio en codigo HTTP y mensaje para el admin.
func traducir(err error) (int, string) {
	var v ErrorValidacion
	switch {
	case errors.As(err, &v):
		return http.StatusBadRequest, v.Mensaje
	case errors.Is(err, ErrCarreraNoExiste):
		return http.StatusNotFound, "La carrera no existe"
	case errors.Is(err, ErrSinCarrera):
		return http.StatusUnprocessableEntity, "No hay ninguna carrera cargada; primero carga los datos de la carrera (HU-04)"
	case errors.Is(err, ErrYaHayMercadoAbierto):
		return http.StatusConflict, "Ya hay un mercado abierto; espera a que cierre para abrir otro"
	default:
		log.Printf("mercados: error inesperado: %v", err)
		return http.StatusInternalServerError, "Error interno"
	}
}
