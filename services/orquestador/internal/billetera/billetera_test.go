package billetera

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
	cuerpo   map[string]any
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

func pedir(t *testing.T, pagos Pagos, metodo, ruta, cuerpo, clave, rol string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, pagos)
	req := httptest.NewRequest(metodo, ruta, strings.NewReader(cuerpo))
	req.Header.Set(sesion.HeaderUsuario, jugador)
	if rol != "" {
		req.Header.Set(sesion.HeaderRol, rol)
	}
	if clave != "" {
		req.Header.Set("Idempotency-Key", clave)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func comprar(t *testing.T, pagos Pagos, cuerpo, clave string) *httptest.ResponseRecorder {
	t.Helper()
	return pedir(t, pagos, http.MethodPost, "/billetera/compras", cuerpo, clave, sesion.RolJugador)
}

func codigo(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var c struct {
		Codigo string `json:"codigo"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	return c.Codigo
}

const compraPagos = `{"compra_id":41,"tokens_acreditados":50,"saldo":1300,"creada_en":"2026-10-26T15:02:11Z"}`

func TestVerTraduceBalanceASaldo(t *testing.T) {
	pagos, f := nuevoPagos(t, `{"user_id":"`+jugador+`","balance":1250,"cop_por_token":1000}`, http.StatusOK)

	rec := pedir(t, pagos, http.MethodGet, "/billetera", "", "", sesion.RolJugador)

	if rec.Code != http.StatusOK {
		t.Fatalf("estado = %d, quiero 200: %s", rec.Code, rec.Body)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"saldo":1250,"cop_por_token":1000}` {
		t.Errorf("cuerpo = %s", got)
	}
	if f.ruta != "GET /internal/wallets/"+jugador || f.tokenRx != token {
		t.Errorf("llamada a Pagos = %q con token %q", f.ruta, f.tokenRx)
	}
}

func TestVerSinRespuestaDePagosEs503YSinBilleteraEs500(t *testing.T) {
	caido, f := nuevoPagos(t, `{}`, http.StatusServiceUnavailable)
	if rec := pedir(t, caido, http.MethodGet, "/billetera", "", "", sesion.RolJugador); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Pagos caido: estado = %d, quiero 503", rec.Code)
	}
	if f.llamadas.Load() != 3 {
		t.Errorf("la lectura debe reintentarse: %d llamadas, quiero 3", f.llamadas.Load())
	}

	sinBilletera, _ := nuevoPagos(t, `{"detail":"Billetera inexistente"}`, http.StatusNotFound)
	rec := pedir(t, sinBilletera, http.MethodGet, "/billetera", "", "", sesion.RolJugador)
	if rec.Code != http.StatusInternalServerError || codigo(t, rec) != "error_interno" {
		t.Errorf("sin billetera: estado = %d codigo = %q, quiero 500 error_interno", rec.Code, codigo(t, rec))
	}
}

func TestComprarArmaLaClaveYDevuelveLaCompra(t *testing.T) {
	pagos, f := nuevoPagos(t, compraPagos, http.StatusCreated)

	rec := comprar(t, pagos, `{"monto_cop":50000}`, "clave-del-front")

	if rec.Code != http.StatusCreated {
		t.Fatalf("estado = %d, quiero 201: %s", rec.Code, rec.Body)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != compraPagos {
		t.Errorf("cuerpo = %s", got)
	}
	if f.ruta != "POST /internal/purchases" || f.clave != "purchase:"+jugador+":clave-del-front" {
		t.Errorf("llamada a Pagos = %q con clave %q", f.ruta, f.clave)
	}
	if f.cuerpo["user_id"] != jugador || f.cuerpo["monto_cop"] != float64(50000) {
		t.Errorf("cuerpo enviado a Pagos = %v", f.cuerpo)
	}
}

func TestReenvioDeLaCompraResponde200(t *testing.T) {
	pagos, _ := nuevoPagos(t, compraPagos, http.StatusOK)
	if rec := comprar(t, pagos, `{"monto_cop":50000}`, "c"); rec.Code != http.StatusOK {
		t.Errorf("estado = %d, quiero 200", rec.Code)
	}
}

func TestReintentaConLaMismaClaveSiPagosFalla(t *testing.T) {
	pagos, f := nuevoPagos(t, compraPagos, http.StatusBadGateway, http.StatusCreated)

	rec := comprar(t, pagos, `{"monto_cop":50000}`, "c")

	if rec.Code != http.StatusCreated || f.llamadas.Load() != 2 {
		t.Errorf("estado = %d tras %d llamadas, quiero 201 tras 2", rec.Code, f.llamadas.Load())
	}
}

func TestErroresDePagosSeTraducen(t *testing.T) {
	casos := []struct {
		estadoPagos int
		estado      int
		codigo      string
	}{
		{http.StatusUnprocessableEntity, http.StatusUnprocessableEntity, "monto_invalido"},
		{http.StatusConflict, http.StatusConflict, "clave_reutilizada"},
		{http.StatusServiceUnavailable, http.StatusServiceUnavailable, "servicio_no_disponible"},
		{http.StatusNotFound, http.StatusInternalServerError, "error_interno"},
		{http.StatusBadRequest, http.StatusInternalServerError, "error_interno"},
	}
	for _, c := range casos {
		pagos, _ := nuevoPagos(t, `{"detail":"x"}`, c.estadoPagos)
		rec := comprar(t, pagos, `{"monto_cop":1000}`, "c")
		if rec.Code != c.estado || codigo(t, rec) != c.codigo {
			t.Errorf("Pagos %d: estado = %d codigo = %q, quiero %d %q", c.estadoPagos, rec.Code, codigo(t, rec), c.estado, c.codigo)
		}
	}
}

func TestPeticionesInvalidasNoLleganAPagos(t *testing.T) {
	casos := []struct {
		nombre, cuerpo, clave, rol string
		estado                     int
		codigo                     string
	}{
		{"sin clave", `{"monto_cop":1000}`, "", sesion.RolJugador, 400, "falta_idempotency_key"},
		{"clave larga", `{"monto_cop":1000}`, strings.Repeat("x", LargoMaximoClave+1), sesion.RolJugador, 400, "falta_idempotency_key"},
		{"sin monto", `{}`, "c", sesion.RolJugador, 400, "cuerpo_invalido"},
		{"monto texto", `{"monto_cop":"mucho"}`, "c", sesion.RolJugador, 400, "cuerpo_invalido"},
		{"monto decimal", `{"monto_cop":1000.5}`, "c", sesion.RolJugador, 400, "cuerpo_invalido"},
		{"no es JSON", `monto`, "c", sesion.RolJugador, 400, "cuerpo_invalido"},
		{"admin", `{"monto_cop":1000}`, "c", sesion.RolAdmin, 403, "solo_jugadores"},
		{"sin rol", `{"monto_cop":1000}`, "c", "", 401, "sin_sesion"},
	}
	for _, c := range casos {
		pagos, f := nuevoPagos(t, compraPagos, http.StatusCreated)
		rec := pedir(t, pagos, http.MethodPost, "/billetera/compras", c.cuerpo, c.clave, c.rol)
		if rec.Code != c.estado || codigo(t, rec) != c.codigo {
			t.Errorf("%s: estado = %d codigo = %q, quiero %d %q", c.nombre, rec.Code, codigo(t, rec), c.estado, c.codigo)
		}
		if f.llamadas.Load() != 0 {
			t.Errorf("%s: no debia llamar a Pagos", c.nombre)
		}
	}
}
