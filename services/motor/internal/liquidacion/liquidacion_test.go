package liquidacion

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arquisoft/motor/internal/apuestas"
	"github.com/arquisoft/motor/internal/mercados"
)

var instante = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// Atajos para las opciones del unico tipo de mercado.
const (
	si = mercados.OpcionSi
	no = mercados.OpcionNo
)

// mercadoPrueba: mercado Si/No cerrado, con observacion de 1 minuto.
func mercadoPrueba() Mercado {
	return Mercado{
		ID: "mercado-1", CarreraID: "carrera-1", Tipo: mercados.TipoCambioPosicion,
		Estado: mercados.EstadoCerrado, Opciones: []string{si, no},
		CierraEn: instante, ObservaHasta: instante.Add(time.Minute),
	}
}

// datosPrueba: hubo un evento de posicion en la ventana, asi que gana "Sí".
func datosPrueba() DatosResolucion {
	return DatosResolucion{HuboEventoPosicion: true, ReproduccionCubre: true}
}

// --- Resolver: regla Si/No ---

func TestResolverSiHuboEventoDePosicion(t *testing.T) {
	// Basta el evento: aunque no haya latido, el evento emitido en la ventana
	// ya prueba que la reproduccion corria y que hubo un cambio.
	for _, cubre := range []bool{true, false} {
		got, err := Resolver(mercadoPrueba(), DatosResolucion{HuboEventoPosicion: true, ReproduccionCubre: cubre})
		if err != nil || got != si {
			t.Errorf("cubre=%v: resultado=%q err=%v, se esperaba %q", cubre, got, err, si)
		}
	}
}

func TestResolverNoSinEventosConReproduccionViva(t *testing.T) {
	got, err := Resolver(mercadoPrueba(), DatosResolucion{HuboEventoPosicion: false, ReproduccionCubre: true})
	if err != nil || got != no {
		t.Fatalf("resultado=%q err=%v, se esperaba %q", got, err, no)
	}
}

// Sin eventos y sin prueba de que la reproduccion corria (nadie la lanzo, se
// cayo o se reinicio en medio de la ventana): no se adivina "No".
func TestResolverSinEventosNiReproduccionQuedaPendiente(t *testing.T) {
	_, err := Resolver(mercadoPrueba(), DatosResolucion{})
	if !errors.Is(err, ErrSinReproduccion) || !errors.Is(err, ErrDatosInsuficientes) {
		t.Fatalf("error = %v, se esperaba %v", err, ErrSinReproduccion)
	}
}

func TestResolverNoAdivinaConMercadoInvalido(t *testing.T) {
	casos := []struct {
		nombre  string
		cambiar func(*Mercado)
		err     error
	}{
		{"otro tipo", func(m *Mercado) { m.Tipo = "driver_first_position" }, ErrTipoNoAdmitido},
		{"sin tipo", func(m *Mercado) { m.Tipo = "" }, ErrTipoNoAdmitido},
		{"aun abierto", func(m *Mercado) { m.Estado = mercados.EstadoAbierto }, ErrDatosInsuficientes},
		{"ya liquidado", func(m *Mercado) { m.Estado = mercados.EstadoLiquidado }, ErrDatosInsuficientes},
		{"sin cierra_en", func(m *Mercado) { m.CierraEn = time.Time{} }, ErrDatosInsuficientes},
		{"ventana vacia", func(m *Mercado) { m.ObservaHasta = m.CierraEn }, ErrDatosInsuficientes},
		{"opciones que no son Si/No", func(m *Mercado) { m.Opciones = []string{"VER", "NOR"} }, ErrDatosInsuficientes},
		{"falta No", func(m *Mercado) { m.Opciones = []string{si} }, ErrDatosInsuficientes},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			m := mercadoPrueba()
			c.cambiar(&m)
			if got, err := Resolver(m, datosPrueba()); !errors.Is(err, c.err) {
				t.Fatalf("resultado=%q error=%v, se esperaba %v", got, err, c.err)
			}
		})
	}
}

type storeFalso struct {
	mu            sync.Mutex
	mercado       Mercado
	datos         DatosResolucion
	contextos     []ContextoApuesta
	retryEn       time.Time
	reclamado     bool
	finalizado    bool
	errorUltimo   string
	fallarGuardar bool
	resumen       LiquidacionMercado
}

func (s *storeFalso) MercadosVencidos(_ context.Context, ahora time.Time) ([]Mercado, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalizado || ahora.Before(s.mercado.ObservaHasta) || (!s.retryEn.IsZero() && ahora.Before(s.retryEn)) {
		return nil, nil
	}
	return []Mercado{s.mercado}, nil
}

func (s *storeFalso) Reclamar(_ context.Context, _ string, ahora time.Time, _ time.Duration) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reclamado || s.finalizado {
		return "", false, nil
	}
	s.reclamado = true
	return "lease", true, nil
}

func (s *storeFalso) Datos(context.Context, Mercado) (DatosResolucion, error) {
	return s.datos, nil
}

func (s *storeFalso) Contextos(context.Context, string) ([]ContextoApuesta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ContextoApuesta(nil), s.contextos...), nil
}

func (s *storeFalso) GuardarResultadoApuesta(_ context.Context, _ string, contexto ContextoApuesta, gano bool, puntos float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fallarGuardar {
		s.fallarGuardar = false
		return errors.New("mongo temporalmente no disponible")
	}
	for i := range s.contextos {
		if s.contextos[i].ApuestaID == contexto.ApuestaID {
			s.contextos[i].Liquidada = true
			s.contextos[i].Gano = &gano
			s.contextos[i].Puntos = puntos
			return nil
		}
	}
	return errors.New("contexto inexistente")
}

func (s *storeFalso) Finalizar(_ context.Context, _ Mercado, token string, resumen LiquidacionMercado, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token != "lease" || !s.reclamado {
		return errors.New("lease invalido")
	}
	s.finalizado = true
	s.reclamado = false
	s.resumen = resumen
	return nil
}

func (s *storeFalso) Reintentar(_ context.Context, _ string, token, diagnostico string, siguiente time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token != "lease" {
		return errors.New("lease invalido")
	}
	s.reclamado = false
	s.retryEn = siguiente
	s.errorUltimo = diagnostico
	return nil
}

type pagosFalso struct {
	mu           sync.Mutex
	efectos      map[string]float64
	llamadas     int
	aplicaciones int
}

func (p *pagosFalso) Liquidar(_ context.Context, l apuestas.Liquidacion) (apuestas.ResultadoLiquidacion, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.llamadas++
	_, existe := p.efectos[l.IdempotencyKey]
	if !existe {
		p.efectos[l.IdempotencyKey] = l.Puntos
		p.aplicaciones++
	}
	return apuestas.ResultadoLiquidacion{
		BetID: l.BetID, Estado: l.Resultado, PuntosOtorgados: l.Puntos,
		Nueva: !existe,
	}, nil
}

func NuevoWorkerPrueba(s Store, p Pagos) *Worker {
	w := NuevoWorker(s, p, nil)
	w.ahora = func() time.Time { return instante.Add(time.Minute) }
	return w
}

func TestLiquidacionMarcaGanadorYPerdedor(t *testing.T) {
	s := &storeFalso{
		mercado: mercadoPrueba(), datos: datosPrueba(),
		contextos: []ContextoApuesta{
			{ApuestaID: "bet-won", Opcion: si, Monto: 100},
			{ApuestaID: "bet-lost", Opcion: no, Monto: 20},
		},
	}
	p := &pagosFalso{efectos: map[string]float64{}}
	w := NuevoWorkerPrueba(s, p)
	if err := w.Procesar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.finalizado || !s.contextos[0].Liquidada || s.contextos[0].Gano == nil || !*s.contextos[0].Gano ||
		s.contextos[0].Puntos != 60 || !s.contextos[1].Liquidada || s.contextos[1].Gano == nil ||
		*s.contextos[1].Gano || s.contextos[1].Puntos != 0 {
		t.Fatalf("resultado inesperado: %+v", s)
	}
	if s.resumen.Resultado != si || s.resumen.PuntosOtorgados != 60 {
		t.Fatalf("resumen de mercado incorrecto: %+v", s.resumen)
	}
	if len(p.efectos) != 2 || p.efectos["bet-settlement:bet-won"] != 60 || p.efectos["bet-settlement:bet-lost"] != 0 {
		t.Fatalf("movimientos de Pagos: %+v", p.efectos)
	}
}

func TestDatosInsuficientesDejanMercadoReintentable(t *testing.T) {
	s := &storeFalso{mercado: mercadoPrueba(), datos: DatosResolucion{}}
	w := NuevoWorkerPrueba(s, &pagosFalso{efectos: map[string]float64{}})
	if err := w.Procesar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.finalizado || s.errorUltimo == "" || s.reclamado {
		t.Fatalf("el mercado no quedó reintentable: %+v", s)
	}
}

func TestContextoInvalidoNoSeClasificaComoPerdedor(t *testing.T) {
	s := &storeFalso{
		mercado: mercadoPrueba(), datos: datosPrueba(),
		contextos: []ContextoApuesta{{ApuestaID: "bet-invalid", Opcion: "otra-opcion", Monto: 100}},
	}
	p := &pagosFalso{efectos: map[string]float64{}}
	w := NuevoWorkerPrueba(s, p)
	if err := w.Procesar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.finalizado || len(p.efectos) != 0 || s.errorUltimo == "" {
		t.Fatalf("contexto inválido debió bloquear la liquidación: store=%+v pagos=%v", s, p.efectos)
	}
}

// Sin eventos y sin latido (p. ej. nadie lanzo la reproduccion): el mercado
// no se liquida, no se llama a Pagos y el diagnostico explica por que.
func TestResolverRechazadoNoSolicitaLiquidacionAFinanzas(t *testing.T) {
	s := &storeFalso{
		mercado:   mercadoPrueba(),
		datos:     DatosResolucion{HuboEventoPosicion: false, ReproduccionCubre: false},
		contextos: []ContextoApuesta{{ApuestaID: "bet-no", Opcion: no, Monto: 100}},
	}
	p := &pagosFalso{efectos: map[string]float64{}}
	w := NuevoWorkerPrueba(s, p)
	if err := w.Procesar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.finalizado || p.llamadas != 0 || s.errorUltimo == "" {
		t.Fatalf("no debe dar \"No\" sin prueba de la reproduccion: mercado=%+v llamadas Pagos=%d", s, p.llamadas)
	}
	if !strings.Contains(s.errorUltimo, "reproduccion") {
		t.Fatalf("diagnóstico poco útil: %q", s.errorUltimo)
	}
}

// Con la reproduccion viva toda la ventana y sin eventos, gana "No": quien
// aposto "No" gana 10 + monto x 0,5 y quien aposto "Sí" pierde.
func TestLiquidacionNoCuandoNoHuboCambios(t *testing.T) {
	s := &storeFalso{
		mercado: mercadoPrueba(),
		datos:   DatosResolucion{HuboEventoPosicion: false, ReproduccionCubre: true},
		contextos: []ContextoApuesta{
			{ApuestaID: "bet-si", Opcion: si, Monto: 40},
			{ApuestaID: "bet-no", Opcion: no, Monto: 40},
		},
	}
	p := &pagosFalso{efectos: map[string]float64{}}
	w := NuevoWorkerPrueba(s, p)
	if err := w.Procesar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.finalizado || s.resumen.Resultado != no || s.resumen.PuntosOtorgados != 30 {
		t.Fatalf("resumen = %+v (finalizado=%v)", s.resumen, s.finalizado)
	}
	if p.efectos["bet-settlement:bet-no"] != 30 || p.efectos["bet-settlement:bet-si"] != 0 {
		t.Fatalf("movimientos de Pagos: %+v", p.efectos)
	}
}

func TestReintentoTrasFalloMongoNoDuplicaEfectosDePagos(t *testing.T) {
	s := &storeFalso{
		mercado: mercadoPrueba(), datos: datosPrueba(),
		contextos:     []ContextoApuesta{{ApuestaID: "bet-won", Opcion: si, Monto: 100}},
		fallarGuardar: true,
	}
	p := &pagosFalso{efectos: map[string]float64{}}
	w := NuevoWorkerPrueba(s, p)
	if err := w.Procesar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.finalizado {
		t.Fatal("no debía finalizar después del fallo Mongo")
	}
	w.ahora = func() time.Time { return instante.Add(70 * time.Second) }
	if err := w.Procesar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.finalizado || p.llamadas != 2 || p.aplicaciones != 1 || len(p.efectos) != 1 || p.efectos["bet-settlement:bet-won"] != 60 {
		t.Fatalf("reintento no idempotente: finalizado=%v llamadas=%d efectos=%v", s.finalizado, p.llamadas, p.efectos)
	}
}

func TestDosWorkersNoReclamanElMismoMercado(t *testing.T) {
	s := &storeFalso{
		mercado: mercadoPrueba(), datos: datosPrueba(),
		contextos: []ContextoApuesta{{ApuestaID: "bet-won", Opcion: si, Monto: 100}},
	}
	p := &pagosFalso{efectos: map[string]float64{}}
	w := NuevoWorkerPrueba(s, p)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Procesar(context.Background()); err != nil {
				t.Errorf("procesar: %v", err)
			}
		}()
	}
	wg.Wait()
	if !s.finalizado || len(p.efectos) != 1 {
		t.Fatalf("liquidación concurrente no segura: finalizado=%v efectos=%v", s.finalizado, p.efectos)
	}
}
