package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNormalizarPosicion verifica que una posición cruda de OpenF1 se
// convierte al formato que espera la colección eventos_carrera.
func TestNormalizarPosicion(t *testing.T) {
	raw := PosicionOpenF1{
		Date:         time.Date(2024, 9, 1, 13, 0, 0, 0, time.UTC),
		DriverNumber: 1,
		Position:     1,
		SessionKey:   9472,
	}

	ev := NormalizarPosicion("2024_monza", raw)

	if ev.CarreraID != "2024_monza" {
		t.Fatalf("carrera_id = %q, quiero 2024_monza", ev.CarreraID)
	}
	if ev.Tipo != "posicion" {
		t.Fatalf("tipo = %q, quiero posicion", ev.Tipo)
	}
	if got := ev.Datos["position"]; got != 1 {
		t.Fatalf("datos.position = %v, quiero 1", got)
	}
	if got := ev.Datos["driver_number"]; got != 1 {
		t.Fatalf("datos.driver_number = %v, quiero 1", got)
	}
	if ev.Timestamp.Location() != time.UTC {
		t.Fatalf("timestamp debe estar en UTC, es %v", ev.Timestamp.Location())
	}
}

// TestOpenF1ClientPosiciones levanta un servidor falso y verifica que el
// cliente parsea bien la respuesta.
func TestOpenF1ClientPosiciones(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/position" {
			t.Errorf("path = %q, quiero /position", r.URL.Path)
		}
		if r.URL.Query().Get("session_key") != "9472" {
			t.Errorf("session_key = %q, quiero 9472", r.URL.Query().Get("session_key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"date":          "2024-09-01T13:00:00Z",
				"driver_number": 1,
				"position":      1,
				"session_key":   9472,
			},
			{
				"date":          "2024-09-01T13:00:01Z",
				"driver_number": 4,
				"position":      2,
				"session_key":   9472,
			},
		})
	}))
	defer srv.Close()

	// Apuntamos el cliente al servidor falso.
	oldBase := baseURLConst
	baseURLConst = srv.URL
	defer func() { baseURLConst = oldBase }()

	c := NuevoOpenF1Client()
	c.http = srv.Client()

	pos, err := c.Posiciones(context.Background(), 9472)
	if err != nil {
		t.Fatalf("Posiciones devolvio error: %v", err)
	}
	if len(pos) != 2 {
		t.Fatalf("len(pos) = %d, quiero 2", len(pos))
	}
	if pos[0].Position != 1 || pos[1].Position != 2 {
		t.Fatalf("posiciones mal parseadas: %+v", pos)
	}
}

// TestOffsetAleatorio: la reproduccion aleatoria empieza despues de las
// primeras vueltas (OffsetMinimo) y deja carrera al final (MargenFinal).
func TestOffsetAleatorio(t *testing.T) {
	carrera := 90 * time.Minute
	maximo := carrera - MargenFinal - OffsetMinimo // azar devuelve [0, maximo)

	if got := OffsetAleatorio(carrera, func(int64) int64 { return 0 }); got != OffsetMinimo {
		t.Errorf("con azar=0 empieza en %s, se esperaba %s", got, OffsetMinimo)
	}
	if got := OffsetAleatorio(carrera, func(n int64) int64 { return n - 1 }); got >= carrera-MargenFinal {
		t.Errorf("con el mayor azar empieza en %s, debe ser antes de %s", got, carrera-MargenFinal)
	}
	var pedido int64
	OffsetAleatorio(carrera, func(n int64) int64 { pedido = n; return 0 })
	if time.Duration(pedido) != maximo {
		t.Errorf("rango pedido al azar = %s, se esperaba %s", time.Duration(pedido), maximo)
	}
}

// TestOffsetAleatorioCarreraCorta: si no alcanza (p. ej. el seed, de pocos
// segundos), se reproduce desde el principio y no se llama al azar.
func TestOffsetAleatorioCarreraCorta(t *testing.T) {
	for _, d := range []time.Duration{0, time.Minute, OffsetMinimo + MargenFinal} {
		got := OffsetAleatorio(d, func(int64) int64 { t.Fatal("no debe sortear"); return 0 })
		if got != 0 {
			t.Errorf("duracion %s: offset %s, se esperaba 0", d, got)
		}
	}
}
