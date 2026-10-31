package fichas

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pagosFalso registra lo que recibe y responde lo configurado.
type pagosFalso struct {
	recibido []FichasFinales
	llamadas int
	err      error
}

func (p *pagosFalso) CerrarEvento(_ context.Context, eventoID string, j []FichasFinales) (ResultadoCierre, error) {
	p.llamadas++
	p.recibido = j
	if p.err != nil {
		return ResultadoCierre{}, p.err
	}
	return ResultadoCierre{EventID: eventoID, Pot: 400, Commission: 40, PrizesTotal: 360, TournamentStatus: "repartido"}, nil
}

func torneoConApuestas(t *testing.T) *Almacen {
	t.Helper()
	a, _ := nuevoAlmacenDePrueba()
	ctx := context.Background()
	if err := a.InicializarTorneo(ctx, evt, []string{"u1", "u2"}, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReservarApuesta(ctx, evt, Reserva{ApuestaID: "b1", UsuarioID: "u1", Monto: 600, Opcion: "Sí", Multiplic: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Liquidar(ctx, evt, "b1", true); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCerrarTorneoEnviaFichasFinalesYLimpiaRedis(t *testing.T) {
	a := torneoConApuestas(t)
	p := &pagosFalso{}
	res, err := a.CerrarTorneo(context.Background(), evt, p)
	if err != nil {
		t.Fatalf("cerrar: %v", err)
	}
	if res.TournamentStatus != "repartido" {
		t.Fatalf("respuesta inesperada: %+v", res)
	}
	got := map[string]FichasFinales{}
	for _, f := range p.recibido {
		got[f.UserID] = f
	}
	// u1: 1000 - 600 + 1200 = 1600 fichas, 600 apostadas; u2 intacto.
	if got["u1"].ChipsFinal != 1600 || got["u1"].ChipsWagered != 600 {
		t.Fatalf("u1 mal reportado: %+v", got["u1"])
	}
	if got["u2"].ChipsFinal != 1000 || got["u2"].ChipsWagered != 0 {
		t.Fatalf("u2 mal reportado: %+v", got["u2"])
	}
	activo, _ := a.store.TorneoActivo(context.Background(), evt)
	if activo {
		t.Fatal("Redis deberia quedar limpio tras el cierre")
	}
}

func TestCerrarTorneoSiPagosFallaNoLimpiaRedis(t *testing.T) {
	a := torneoConApuestas(t)
	p := &pagosFalso{err: ErrPagosNoDisponible}
	if _, err := a.CerrarTorneo(context.Background(), evt, p); !errors.Is(err, ErrPagosNoDisponible) {
		t.Fatalf("quiero ErrPagosNoDisponible, tengo %v", err)
	}
	activo, _ := a.store.TorneoActivo(context.Background(), evt)
	if !activo {
		t.Fatal("si Pagos falla, Redis debe quedar intacto para reintentar")
	}
	// El reintento, con Pagos ya disponible, cierra y limpia.
	p.err = nil
	if _, err := a.CerrarTorneo(context.Background(), evt, p); err != nil {
		t.Fatalf("reintento: %v", err)
	}
}

func TestPagosCierreHTTPEnviaClaveYValidaElReparto(t *testing.T) {
	var clave, token, ruta string
	var cuerpo map[string][]FichasFinales
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clave, token, ruta = r.Header.Get("Idempotency-Key"), r.Header.Get("X-Internal-Service-Token"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&cuerpo)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"event_id":"e1","status":"finalizado","tournament_status":"repartido","pot":400,"commission":40,"prizes_total":350,"residue":10,"players":[],"nueva":true}`))
	}))
	defer srv.Close()

	p := NuevoPagosCierreHTTP(srv.URL+"/", "token-de-prueba")
	res, err := p.CerrarEvento(context.Background(), "e1", []FichasFinales{{UserID: "u1", ChipsFinal: 5, ChipsWagered: 3}})
	if err != nil {
		t.Fatalf("cerrar: %v", err)
	}
	if clave != "event-close:e1" || token != "token-de-prueba" || ruta != "/internal/events/e1/close" {
		t.Fatalf("cabeceras/ruta: clave=%q token=%q ruta=%q", clave, token, ruta)
	}
	if len(cuerpo["players"]) != 1 || cuerpo["players"][0].UserID != "u1" {
		t.Fatalf("cuerpo enviado: %+v", cuerpo)
	}
	if res.Residue != 10 || !res.Nueva {
		t.Fatalf("respuesta: %+v", res)
	}
}

func TestPagosCierreHTTPRechazaRepartoQueNoCuadra(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pot":400,"commission":40,"prizes_total":360,"residue":5}`))
	}))
	defer srv.Close()
	_, err := NuevoPagosCierreHTTP(srv.URL, "t").CerrarEvento(context.Background(), "e1", nil)
	if !errors.Is(err, ErrPagosNoDisponible) {
		t.Fatalf("quiero ErrPagosNoDisponible por reparto descuadrado, tengo %v", err)
	}
}

func TestPagosCierreHTTPTraduceConflicto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"Los jugadores enviados no coinciden con los inscritos"}`))
	}))
	defer srv.Close()
	_, err := NuevoPagosCierreHTTP(srv.URL, "t").CerrarEvento(context.Background(), "e1", nil)
	if !errors.Is(err, ErrCierreRechazado) || !strings.Contains(err.Error(), "no coinciden") {
		t.Fatalf("quiero ErrCierreRechazado con el detalle, tengo %v", err)
	}
}
