package fichas

import (
	"context"
	"testing"
	"time"
)

// storeMemoria es un doble de Store para probar la logica sin Redis. Replica
// la semantica importante: escribir reservas y resultados una sola vez y
// recalcular el saldo sin acumular.
type storeMemoria struct {
	inicial    map[string]int64              // evento -> fichas iniciales
	inscritos  map[string]map[string]bool    // evento -> set de usuarios
	reservas   map[string]map[string]Reserva // evento -> apuesta -> reserva
	resultados map[string]map[string]ResultadoApuesta
}

func nuevoStoreMemoria() *storeMemoria {
	return &storeMemoria{
		inicial:    map[string]int64{},
		inscritos:  map[string]map[string]bool{},
		reservas:   map[string]map[string]Reserva{},
		resultados: map[string]map[string]ResultadoApuesta{},
	}
}

func (s *storeMemoria) InicializarJugadores(_ context.Context, e string, jugadores []string, fichas int64) error {
	if _, ya := s.inicial[e]; ya {
		return nil // idempotente: no reinicia
	}
	s.inicial[e] = fichas
	s.inscritos[e] = map[string]bool{}
	for _, j := range jugadores {
		s.inscritos[e][j] = true
	}
	return nil
}

func (s *storeMemoria) TorneoActivo(_ context.Context, e string) (bool, error) {
	_, ok := s.inicial[e]
	return ok, nil
}

func (s *storeMemoria) GuardarReserva(_ context.Context, e string, r Reserva) (bool, error) {
	if s.reservas[e] == nil {
		s.reservas[e] = map[string]Reserva{}
	}
	if _, existe := s.reservas[e][r.ApuestaID]; existe {
		return false, nil
	}
	s.reservas[e][r.ApuestaID] = r
	return true, nil
}

func (s *storeMemoria) Reserva(_ context.Context, e, a string) (Reserva, bool, error) {
	r, ok := s.reservas[e][a]
	return r, ok, nil
}

func (s *storeMemoria) GuardarResultado(_ context.Context, e, a string, res ResultadoApuesta) (bool, error) {
	if s.resultados[e] == nil {
		s.resultados[e] = map[string]ResultadoApuesta{}
	}
	if _, existe := s.resultados[e][a]; existe {
		return false, nil
	}
	s.resultados[e][a] = res
	return true, nil
}

func (s *storeMemoria) Recalcular(_ context.Context, e, u string) (Estado, error) {
	saldo := s.inicial[e]
	var apostado int64
	for a, r := range s.reservas[e] {
		if r.UsuarioID != u {
			continue
		}
		apostado += r.Monto
		saldo -= r.Monto
		if res, ok := s.resultados[e][a]; ok {
			saldo += res.FichasPagadas
		}
	}
	if saldo < 0 {
		saldo = 0
	}
	return Estado{UsuarioID: u, Fichas: saldo, Apostado: apostado}, nil
}

func (s *storeMemoria) Estados(_ context.Context, e string) ([]Estado, error) {
	var out []Estado
	for u := range s.inscritos[e] {
		st, _ := s.Recalcular(context.Background(), e, u)
		out = append(out, st)
	}
	return out, nil
}

func (s *storeMemoria) Limpiar(_ context.Context, e string) error {
	delete(s.inicial, e)
	delete(s.inscritos, e)
	delete(s.reservas, e)
	delete(s.resultados, e)
	return nil
}

func nuevoAlmacenDePrueba() (*Almacen, *storeMemoria) {
	st := nuevoStoreMemoria()
	a := NuevoAlmacen(st)
	a.ahora = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	return a, st
}

const evt = "evento-1"

func TestInicializarTorneoDaFichasYEsIdempotente(t *testing.T) {
	a, _ := nuevoAlmacenDePrueba()
	ctx := context.Background()
	if err := a.InicializarTorneo(ctx, evt, []string{"u1", "u2"}, 1000); err != nil {
		t.Fatalf("inicializar: %v", err)
	}
	e, err := a.EstadoDe(ctx, evt, "u1")
	if err != nil {
		t.Fatalf("estado: %v", err)
	}
	if e.Fichas != 1000 {
		t.Fatalf("fichas iniciales: quiero 1000, tengo %d", e.Fichas)
	}
	// Reinicializar no debe cambiar las fichas de u1.
	if err := a.InicializarTorneo(ctx, evt, []string{"u1", "u2"}, 5000); err != nil {
		t.Fatalf("reinicializar: %v", err)
	}
	e, _ = a.EstadoDe(ctx, evt, "u1")
	if e.Fichas != 1000 {
		t.Fatalf("reinicializar cambio las fichas: tengo %d", e.Fichas)
	}
}

func TestReservarDescuentaUnaVezAunConReintento(t *testing.T) {
	a, _ := nuevoAlmacenDePrueba()
	ctx := context.Background()
	_ = a.InicializarTorneo(ctx, evt, []string{"u1"}, 1000)

	r := Reserva{ApuestaID: "b1", UsuarioID: "u1", Monto: 300, Opcion: "Sí", Multiplic: 2.0}
	e, err := a.ReservarApuesta(ctx, evt, r)
	if err != nil {
		t.Fatalf("reservar: %v", err)
	}
	if e.Fichas != 700 || e.Apostado != 300 {
		t.Fatalf("tras reservar: fichas=%d apostado=%d (quiero 700/300)", e.Fichas, e.Apostado)
	}
	// Reintento con la misma apuesta: no vuelve a descontar (RNF-12).
	e, err = a.ReservarApuesta(ctx, evt, r)
	if err != nil {
		t.Fatalf("reservar reintento: %v", err)
	}
	if e.Fichas != 700 || e.Apostado != 300 {
		t.Fatalf("reintento descontó de nuevo: fichas=%d apostado=%d", e.Fichas, e.Apostado)
	}
}

func TestReservarRechazaContenidoDistinto(t *testing.T) {
	a, _ := nuevoAlmacenDePrueba()
	ctx := context.Background()
	_ = a.InicializarTorneo(ctx, evt, []string{"u1"}, 1000)
	_, _ = a.ReservarApuesta(ctx, evt, Reserva{ApuestaID: "b1", UsuarioID: "u1", Monto: 300, Opcion: "Sí", Multiplic: 2})
	_, err := a.ReservarApuesta(ctx, evt, Reserva{ApuestaID: "b1", UsuarioID: "u1", Monto: 500, Opcion: "No", Multiplic: 2})
	if err != ErrReservaIncompatible {
		t.Fatalf("quiero ErrReservaIncompatible, tengo %v", err)
	}
}

func TestReservarRechazaSinFichas(t *testing.T) {
	a, _ := nuevoAlmacenDePrueba()
	ctx := context.Background()
	_ = a.InicializarTorneo(ctx, evt, []string{"u1"}, 1000)
	_, err := a.ReservarApuesta(ctx, evt, Reserva{ApuestaID: "b1", UsuarioID: "u1", Monto: 1500, Opcion: "Sí", Multiplic: 2})
	if err != ErrFichasInsuficientes {
		t.Fatalf("quiero ErrFichasInsuficientes, tengo %v", err)
	}
}

func TestLiquidarGanaPagaMontoPorMultiplicador(t *testing.T) {
	a, _ := nuevoAlmacenDePrueba()
	ctx := context.Background()
	_ = a.InicializarTorneo(ctx, evt, []string{"u1"}, 1000)
	_, _ = a.ReservarApuesta(ctx, evt, Reserva{ApuestaID: "b1", UsuarioID: "u1", Monto: 300, Opcion: "Sí", Multiplic: 2.5})

	// Gana: recibe 300 x 2.5 = 750. Saldo: 1000 - 300 + 750 = 1450.
	e, err := a.Liquidar(ctx, evt, "b1", true)
	if err != nil {
		t.Fatalf("liquidar: %v", err)
	}
	if e.Fichas != 1450 {
		t.Fatalf("tras ganar: quiero 1450 fichas, tengo %d", e.Fichas)
	}
	// Liquidar de nuevo no paga doble (RNF-12).
	e, err = a.Liquidar(ctx, evt, "b1", true)
	if err != nil {
		t.Fatalf("liquidar reintento: %v", err)
	}
	if e.Fichas != 1450 {
		t.Fatalf("doble liquidacion pagó de más: tengo %d", e.Fichas)
	}
}

func TestLiquidarPierdeNoDevuelveNada(t *testing.T) {
	a, _ := nuevoAlmacenDePrueba()
	ctx := context.Background()
	_ = a.InicializarTorneo(ctx, evt, []string{"u1"}, 1000)
	_, _ = a.ReservarApuesta(ctx, evt, Reserva{ApuestaID: "b1", UsuarioID: "u1", Monto: 300, Opcion: "Sí", Multiplic: 2})
	e, err := a.Liquidar(ctx, evt, "b1", false)
	if err != nil {
		t.Fatalf("liquidar: %v", err)
	}
	// Pierde: saldo 1000 - 300 = 700; apostado sigue en 300.
	if e.Fichas != 700 || e.Apostado != 300 {
		t.Fatalf("tras perder: fichas=%d apostado=%d (quiero 700/300)", e.Fichas, e.Apostado)
	}
}

func TestLiquidarSinReservaFalla(t *testing.T) {
	a, _ := nuevoAlmacenDePrueba()
	ctx := context.Background()
	_ = a.InicializarTorneo(ctx, evt, []string{"u1"}, 1000)
	_, err := a.Liquidar(ctx, evt, "fantasma", true)
	if err != ErrJugadorNoInscrito {
		t.Fatalf("quiero ErrJugadorNoInscrito, tengo %v", err)
	}
}

func TestReservarEnTorneoNoActivoFalla(t *testing.T) {
	a, _ := nuevoAlmacenDePrueba()
	_, err := a.ReservarApuesta(context.Background(), "inexistente", Reserva{ApuestaID: "b1", UsuarioID: "u1", Monto: 10, Opcion: "Sí", Multiplic: 2})
	if err != ErrTorneoNoActivo {
		t.Fatalf("quiero ErrTorneoNoActivo, tengo %v", err)
	}
}
