package inscripcion

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arquisoft/orquestador/internal/interno"
	"github.com/arquisoft/orquestador/internal/sesion"
)

const (
	evento  = "11111111-2222-4333-8444-555555555555"
	jugador = "0b6c2f1e-8d4a-4c3b-9f2e-1a2b3c4d5e6f"
	token   = "token-interno-de-pruebas-orquestador"
)

// pagosFalso responde con los estados de la lista, uno por llamada (el ultimo
// se repite), y guarda lo que recibio.
type pagosFalso struct {
	llamadas atomic.Int32
	ruta     string
	clave    string
	tokenRx  string
	cuerpo   map[string]string
}

func nuevoPagos(t *testing.T, cuerpo string, estados ...int) (*interno.Cliente, *pagosFalso) {
	t.Helper()
	f := &pagosFalso{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(f.llamadas.Add(1))
		f.ruta = r.Method + " " + r.URL.Path
		f.clave = r.Header.Get("Idempotency-Key")
		f.tokenRx = r.Header.Get("X-Internal-Service-Token")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &f.cuerpo)
		w.WriteHeader(estados[min(n, len(estados))-1])
		_, _ = w.Write([]byte(cuerpo))
	}))
	t.Cleanup(srv.Close)
	c := interno.Nuevo(srv.URL, token)
	c.Esperas = []time.Duration{time.Millisecond, time.Millisecond}
	return c, f
}

func pedir(t *testing.T, pagos Pagos, eventoID, usuario, rol string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, pagos)
	req := httptest.NewRequest(http.MethodPost, "/eventos/"+eventoID+"/inscripcion", nil)
	if usuario != "" {
		req.Header.Set(sesion.HeaderUsuario, usuario)
	}
	if rol != "" {
		req.Header.Set(sesion.HeaderRol, rol)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func codigo(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var c struct {
		Codigo string `json:"codigo"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	return c.Codigo
}

const okPagos = `{"event_id":"` + evento + `","user_id":"` + jugador + `","tournament_status":"abierto","refunded":false,` +
	`"buy_in":100,"initial_chips":1000,"pot":400,"saldo":1150,"nueva":true}`

func TestInscribirLlamaAPagosConLaClaveYTraduceLaRespuesta(t *testing.T) {
	pagos, f := nuevoPagos(t, okPagos, http.StatusCreated)

	rec := pedir(t, pagos, evento, jugador, sesion.RolJugador)

	if rec.Code != http.StatusCreated {
		t.Fatalf("estado = %d, cuerpo %s", rec.Code, rec.Body)
	}
	if f.ruta != "POST /internal/events/"+evento+"/registrations" {
		t.Errorf("ruta = %q", f.ruta)
	}
	if f.clave != "event-entry:"+evento+":"+jugador || f.tokenRx != token || f.cuerpo["user_id"] != jugador {
		t.Errorf("clave %q, token %q, cuerpo %v", f.clave, f.tokenRx, f.cuerpo)
	}
	var r Respuesta
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	if r.EventoID != evento || r.YaInscrito || r.CostoInscripcion != 100 || r.FichasIniciales != 1000 || r.Botin != 400 || r.Saldo == nil || *r.Saldo != 1150 {
		t.Errorf("respuesta = %+v", r)
	}
}

func TestYaInscritoResponde200SinCobrar(t *testing.T) {
	pagos, _ := nuevoPagos(t, strings.Replace(okPagos, `"nueva":true`, `"nueva":false`, 1), http.StatusOK)

	rec := pedir(t, pagos, evento, jugador, sesion.RolJugador)

	var r Respuesta
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	if rec.Code != http.StatusOK || !r.YaInscrito {
		t.Fatalf("estado = %d, respuesta %+v", rec.Code, r)
	}
}

func TestTraduceLosErroresDePagos(t *testing.T) {
	casos := []struct {
		pagos  int
		estado int
		codigo string
	}{
		{http.StatusNotFound, http.StatusNotFound, "evento_no_existe"},
		{http.StatusConflict, http.StatusConflict, "inscripciones_cerradas"},
		{http.StatusUnprocessableEntity, http.StatusUnprocessableEntity, "saldo_insuficiente"},
		{http.StatusBadRequest, http.StatusInternalServerError, "error_interno"},
		{http.StatusUnauthorized, http.StatusInternalServerError, "error_interno"},
	}
	for _, c := range casos {
		pagos, _ := nuevoPagos(t, `{"detail":"x"}`, c.pagos)
		rec := pedir(t, pagos, evento, jugador, sesion.RolJugador)
		if rec.Code != c.estado || codigo(t, rec) != c.codigo {
			t.Errorf("Pagos %d -> %d %s, se esperaba %d %s", c.pagos, rec.Code, codigo(t, rec), c.estado, c.codigo)
		}
	}
}

func TestReintentaSiPagosFallaYLuegoResponde(t *testing.T) {
	pagos, f := nuevoPagos(t, okPagos, http.StatusServiceUnavailable, http.StatusCreated)

	rec := pedir(t, pagos, evento, jugador, sesion.RolJugador)

	if rec.Code != http.StatusCreated || f.llamadas.Load() != 2 {
		t.Fatalf("estado = %d tras %d llamadas", rec.Code, f.llamadas.Load())
	}
}

func TestSiPagosNoRespondeEs503(t *testing.T) {
	pagos, f := nuevoPagos(t, "", http.StatusServiceUnavailable)

	rec := pedir(t, pagos, evento, jugador, sesion.RolJugador)

	if rec.Code != http.StatusServiceUnavailable || codigo(t, rec) != "servicio_no_disponible" || f.llamadas.Load() != 3 {
		t.Fatalf("estado = %d %s tras %d llamadas", rec.Code, codigo(t, rec), f.llamadas.Load())
	}
}

func TestSesionYRolAntesDeLlamarAPagos(t *testing.T) {
	pagos, f := nuevoPagos(t, okPagos, http.StatusCreated)

	if rec := pedir(t, pagos, evento, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("sin sesion = %d", rec.Code)
	}
	if rec := pedir(t, pagos, evento, jugador, sesion.RolAdmin); rec.Code != http.StatusForbidden {
		t.Errorf("admin = %d", rec.Code)
	}
	if f.llamadas.Load() != 0 {
		t.Errorf("no debia llamar a Pagos y lo llamo %d veces", f.llamadas.Load())
	}
}

func TestEventoConIdInvalidoEs404SinLlamarAPagos(t *testing.T) {
	pagos, f := nuevoPagos(t, okPagos, http.StatusCreated)

	rec := pedir(t, pagos, "no-es-uuid", jugador, sesion.RolJugador)

	if rec.Code != http.StatusNotFound || codigo(t, rec) != "evento_no_existe" || f.llamadas.Load() != 0 {
		t.Fatalf("estado = %d %s, llamadas %d", rec.Code, codigo(t, rec), f.llamadas.Load())
	}
}
