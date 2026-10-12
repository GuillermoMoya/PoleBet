package apuestas

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const usuario = "3f2b8c1e-9a4d-4e6f-8b1a-2c3d4e5f6a7b"

var ahora = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

type mercadosFalsos map[string]Mercado

func (m mercadosFalsos) Obtener(_ context.Context, id string) (Mercado, error) {
	if x, ok := m[id]; ok {
		return x, nil
	}
	return Mercado{}, ErrMercadoNoExiste
}

type contextosFalsos struct {
	mu    sync.Mutex
	items []Contexto
	err   error
}

func (c *contextosFalsos) Guardar(_ context.Context, x Contexto) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.items = append(c.items, x)
	return nil
}

func nuevo(t *testing.T, saldo int64) (*Servicio, *PagosMock, *contextosFalsos) {
	t.Helper()
	pagos := NuevoPagosMock(saldo)
	ctxs := &contextosFalsos{}
	svc := NuevoServicio(mercadosFalsos{
		"abierto": {ID: "abierto", Estado: EstadoAbierto, Opciones: []string{"VER", "NOR"}, CierraEn: ahora.Add(30 * time.Second)},
		"vencido": {ID: "vencido", Estado: EstadoAbierto, Opciones: []string{"VER", "NOR"}, CierraEn: ahora.Add(-time.Second)},
		"cerrado": {ID: "cerrado", Estado: "cerrado", Opciones: []string{"VER", "NOR"}, CierraEn: ahora.Add(time.Hour)},
	}, pagos, ctxs)
	svc.Ahora = func() time.Time { return ahora }
	return svc, pagos, ctxs
}

func TestApuestaValida(t *testing.T) {
	svc, pagos, ctxs := nuevo(t, 1000)

	a, err := svc.Apostar(context.Background(), usuario, Peticion{"abierto", "VER", 100}, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.PuntosPotenciales != 60 { // 10 + 100*0,5
		t.Errorf("puntos = %v, se esperaba 60", a.PuntosPotenciales)
	}
	if pagos.Saldo(usuario) != 900 {
		t.Errorf("saldo = %d, se esperaba 900", pagos.Saldo(usuario))
	}
	if len(ctxs.items) != 1 || ctxs.items[0].ApuestaID != a.ID {
		t.Errorf("contexto no guardado: %+v", ctxs.items)
	}
}

func TestRechazos(t *testing.T) {
	svc, pagos, _ := nuevo(t, 50)
	casos := []struct {
		nombre string
		p      Peticion
		quiero error
	}{
		{"monto cero", Peticion{"abierto", "VER", 0}, ErrPeticionInvalida},
		{"monto negativo", Peticion{"abierto", "VER", -5}, ErrPeticionInvalida},
		{"sin opcion", Peticion{"abierto", " ", 10}, ErrPeticionInvalida},
		{"mercado inexistente", Peticion{"nada", "VER", 10}, ErrMercadoNoExiste},
		{"mercado cerrado por estado", Peticion{"cerrado", "VER", 10}, ErrMercadoCerrado},
		{"ventana vencida segun el servidor", Peticion{"vencido", "VER", 10}, ErrMercadoCerrado},
		{"opcion ajena al mercado", Peticion{"abierto", "HAM", 10}, ErrOpcionInvalida},
		{"saldo insuficiente", Peticion{"abierto", "VER", 51}, ErrSaldoInsuficiente},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if _, err := svc.Apostar(context.Background(), usuario, c.p, ""); !errors.Is(err, c.quiero) {
				t.Errorf("error = %v, se esperaba %v", err, c.quiero)
			}
		})
	}
	if pagos.Saldo(usuario) != 50 {
		t.Errorf("un rechazo no debe tocar el saldo: %d", pagos.Saldo(usuario))
	}
}

// El cierre lo decide el reloj del servidor justo en el limite (RNF-05).
func TestCierreExactoRechaza(t *testing.T) {
	svc, _, _ := nuevo(t, 100)
	svc.Ahora = func() time.Time { return ahora.Add(30 * time.Second) } // = cierra_en
	if _, err := svc.Apostar(context.Background(), usuario, Peticion{"abierto", "VER", 10}, ""); !errors.Is(err, ErrMercadoCerrado) {
		t.Errorf("error = %v", err)
	}
}

// RNF-01: dos o mas apuestas simultaneas que juntas superan el saldo.
func TestConcurrenciaNoDejaSaldoNegativo(t *testing.T) {
	svc, pagos, _ := nuevo(t, 100)

	var ok, sinSaldo int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Apostar(context.Background(), usuario, Peticion{"abierto", "VER", 30}, "")
			switch {
			case err == nil:
				atomic.AddInt32(&ok, 1)
			case errors.Is(err, ErrSaldoInsuficiente):
				atomic.AddInt32(&sinSaldo, 1)
			default:
				t.Errorf("error inesperado: %v", err)
			}
		}()
	}
	wg.Wait()

	if ok != 3 || sinSaldo != 17 {
		t.Errorf("aceptadas=%d rechazadas=%d, se esperaba 3 y 17", ok, sinSaldo)
	}
	if s := pagos.Saldo(usuario); s != 10 {
		t.Errorf("saldo final = %d, se esperaba 10", s)
	}
}

// RNF-02: reenviar con la misma clave no debita dos veces.
func TestIdempotenciaConClaveDelCliente(t *testing.T) {
	svc, pagos, _ := nuevo(t, 100)

	a1, err1 := svc.Apostar(context.Background(), usuario, Peticion{"abierto", "VER", 40}, "clic-1")
	a2, err2 := svc.Apostar(context.Background(), usuario, Peticion{"abierto", "VER", 40}, "clic-1")
	if err1 != nil || err2 != nil {
		t.Fatalf("errores: %v, %v", err1, err2)
	}
	if a1.ID != a2.ID {
		t.Errorf("deberia ser la misma apuesta: %s vs %s", a1.ID, a2.ID)
	}
	if pagos.Saldo(usuario) != 60 {
		t.Errorf("saldo = %d, se esperaba 60 (un solo debito)", pagos.Saldo(usuario))
	}

	a3, _ := svc.Apostar(context.Background(), usuario, Peticion{"abierto", "VER", 40}, "")
	if a3.ID == a1.ID {
		t.Error("sin clave cada apuesta debe ser nueva")
	}
}

func TestFalloAlGuardarContexto(t *testing.T) {
	svc, _, ctxs := nuevo(t, 100)
	ctxs.err = errors.New("mongo caido")
	if _, err := svc.Apostar(context.Background(), usuario, Peticion{"abierto", "VER", 10}, ""); err == nil {
		t.Error("deberia informar el error")
	}
}

// --- PagosHTTP contra un servidor falso ---

func TestPagosHTTPTraduceCodigos(t *testing.T) {
	casos := []struct {
		status int
		quiero error
	}{
		{200, nil},
		{201, nil},
		{409, ErrPagosConflicto},
		{422, ErrSaldoInsuficiente},
		{404, ErrUsuarioNoExiste},
		{500, ErrPagosNoDisponible},
	}
	for _, c := range casos {
		var ruta, clave string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ruta, clave = r.URL.Path, r.Header.Get("Idempotency-Key")
			if r.Header.Get("X-Internal-Service-Token") != "token-prueba-para-liquidacion-hu07" {
				t.Errorf("falta el token de servicio")
			}
			w.WriteHeader(c.status)
		}))
		err := NuevoPagosHTTP(srv.URL, "token-prueba-para-liquidacion-hu07").Debitar(context.Background(), Debito{BetID: "b", IdempotencyKey: "k1"})
		srv.Close()

		if !errors.Is(err, c.quiero) {
			t.Errorf("status %d: error = %v, se esperaba %v", c.status, err, c.quiero)
		}
		if ruta != RutaDebito || clave != "k1" {
			t.Errorf("status %d: ruta=%q clave=%q", c.status, ruta, clave)
		}
	}
}

func TestPagosHTTPSinConexion(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nadie escucha
	if err := NuevoPagosHTTP(url, "token-prueba-para-liquidacion-hu07").Debitar(context.Background(), Debito{}); !errors.Is(err, ErrPagosNoDisponible) {
		t.Errorf("error = %v", err)
	}
}

func TestPagosHTTPLiquidarContrato(t *testing.T) {
	const betID = "3f2b8c1e-9a4d-4e6f-8b1a-2c3d4e5f6a7b"
	const key = "bet-settlement:" + betID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != RutaLiquidacion {
			t.Errorf("petición inesperada: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") != key ||
			r.Header.Get("X-Internal-Service-Token") != "token-prueba-para-liquidacion-hu07" {
			t.Errorf("faltan headers de integración interna")
		}
		_, _ = w.Write([]byte(`{"bet_id":"` + betID + `","status":"won","points_awarded":60.5,"points_total":80.5,"nueva":false}`))
	}))
	defer srv.Close()

	resultado, err := NuevoPagosHTTP(srv.URL, "token-prueba-para-liquidacion-hu07").Liquidar(
		context.Background(),
		Liquidacion{BetID: betID, Resultado: "won", Puntos: 60.5, IdempotencyKey: key},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resultado.BetID != betID || resultado.Estado != "won" ||
		resultado.PuntosOtorgados != 60.5 || resultado.PuntosTotales != 80.5 || resultado.Nueva {
		t.Fatalf("respuesta inesperada: %+v", resultado)
	}
}

func TestPagosHTTPLiquidarConflictoYErrorDeValidacion(t *testing.T) {
	for _, caso := range []struct {
		status int
		want   error
	}{
		{http.StatusConflict, ErrPagosConflicto},
		{http.StatusUnprocessableEntity, ErrPagosNoDisponible},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(caso.status)
		}))
		_, err := NuevoPagosHTTP(srv.URL, "token-prueba-para-liquidacion-hu07").Liquidar(
			context.Background(),
			Liquidacion{BetID: "bet", Resultado: "lost", IdempotencyKey: "key"},
		)
		srv.Close()
		if !errors.Is(err, caso.want) {
			t.Errorf("status %d: error=%v, esperado=%v", caso.status, err, caso.want)
		}
	}
}

// --- Handler ---

func montar(t *testing.T) (http.Handler, *PagosMock) {
	svc, pagos, _ := nuevo(t, 100)
	mux := http.NewServeMux()
	Register(mux, svc,
		func(h http.Handler) http.Handler { return h },
		func(r *http.Request) (string, bool) {
			u := r.Header.Get("X-Test-User")
			return u, u != ""
		})
	return mux, pagos
}

func post(h http.Handler, user, cuerpo string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/apuestas", strings.NewReader(cuerpo))
	if user != "" {
		r.Header.Set("X-Test-User", user)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandlerCodigos(t *testing.T) {
	h, _ := montar(t)
	casos := []struct {
		nombre, user, cuerpo string
		status               int
		contiene             string
	}{
		{"ok", usuario, `{"market_id":"abierto","option":"VER","amount":10}`, 201, "potential_points"},
		{"sin sesion", "", `{}`, 401, "no autorizado"},
		{"json roto", usuario, `{`, 400, "cuerpo invalido"},
		{"campo desconocido", usuario, `{"market_id":"abierto","option":"VER","amount":10,"user_id":"otro"}`, 400, "cuerpo invalido"},
		{"monto cero", usuario, `{"market_id":"abierto","option":"VER","amount":0}`, 400, "mayor que 0"},
		{"mercado cerrado", usuario, `{"market_id":"vencido","option":"VER","amount":10}`, 409, "Mercado cerrado"},
		{"saldo insuficiente", usuario, `{"market_id":"abierto","option":"VER","amount":999}`, 422, "Saldo insuficiente"},
		{"mercado inexistente", usuario, `{"market_id":"zzz","option":"VER","amount":10}`, 404, "no existe"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			w := post(h, c.user, c.cuerpo)
			if w.Code != c.status || !strings.Contains(w.Body.String(), c.contiene) {
				t.Errorf("status=%d cuerpo=%s", w.Code, w.Body.String())
			}
		})
	}
}

// El usuario sale SIEMPRE de la sesion, nunca del cuerpo.
func TestHandlerUsaUsuarioDeLaSesion(t *testing.T) {
	h, pagos := montar(t)
	post(h, usuario, `{"market_id":"abierto","option":"VER","amount":10}`)
	if len(pagos.Debitos) != 1 || pagos.Debitos[0].UserID != usuario {
		t.Errorf("debitos = %+v", pagos.Debitos)
	}
}

func TestPuntosPotenciales(t *testing.T) {
	if got := PuntosPotenciales(100); got != 60 {
		t.Errorf("got %v", got)
	}
}
