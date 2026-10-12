package liquidacion

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arquisoft/motor/internal/apuestas"
)

var instante = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func mercadoPrueba() Mercado {
	return Mercado{
		ID: "mercado-1", CarreraID: "carrera-1", Tipo: TipoPilotoPrimero,
		Estado: "cerrado", Opciones: []string{"Max Verstappen", "Lando Norris"},
		CierraEn: instante, ObservaHasta: instante.Add(time.Minute),
	}
}

func datosPrueba() DatosResolucion {
	return DatosResolucion{
		Muestras: []MuestraPosicion{
			{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 1, Position: 2},
			{Timestamp: instante.Add(2 * time.Second), LeidoEn: instante.Add(2 * time.Second), DriverNumber: 1, Position: 1},
			{Timestamp: instante.Add(2 * time.Second), LeidoEn: instante.Add(2 * time.Second), DriverNumber: 4, Position: 2},
		},
		Pilotos: []Piloto{
			{DriverNumber: 1, FullName: "Max Verstappen", NameAcronym: "VER"},
			{DriverNumber: 4, FullName: "Lando Norris", NameAcronym: "NOR"},
		},
	}
}

func TestResolverPilotoPrimeroYLimitesInclusivos(t *testing.T) {
	m := mercadoPrueba()
	datos := datosPrueba()
	datos.Muestras = append(datos.Muestras,
		MuestraPosicion{Timestamp: m.CierraEn, LeidoEn: m.CierraEn, DriverNumber: 4, Position: 3},
		MuestraPosicion{Timestamp: m.ObservaHasta, LeidoEn: m.ObservaHasta, DriverNumber: 1, Position: 1},
	)
	resultado, err := Resolver(m, datos)
	if err != nil {
		t.Fatal(err)
	}
	if resultado != "Max Verstappen" {
		t.Fatalf("resultado %q", resultado)
	}
}

func TestResolverNoAdivinaConDatosFaltantesInvalidosOAjenosALaVentana(t *testing.T) {
	m := mercadoPrueba()
	casos := []struct {
		nombre string
		datos  DatosResolucion
	}{
		{"sin muestras", DatosResolucion{Pilotos: datosPrueba().Pilotos}},
		{"falta un piloto del roster", DatosResolucion{
			Muestras: []MuestraPosicion{{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 1, Position: 1}},
			Pilotos:  datosPrueba().Pilotos,
		}},
		{"posición inválida", DatosResolucion{
			Muestras: []MuestraPosicion{{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 0, Position: 1}},
			Pilotos: datosPrueba().Pilotos,
		}},
		{"muestras fuera de ventana", DatosResolucion{
			Muestras: []MuestraPosicion{{Timestamp: instante.Add(-time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 1, Position: 1}},
			Pilotos: datosPrueba().Pilotos,
		}},
		{"muestra leída tarde", DatosResolucion{
			Muestras: []MuestraPosicion{{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Minute + time.Second), DriverNumber: 1, Position: 1}},
			Pilotos:  datosPrueba().Pilotos,
		}},
		{"líder ambiguo", DatosResolucion{
			Muestras: []MuestraPosicion{
				{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 1, Position: 1},
				{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 4, Position: 1},
			},
			Pilotos: datosPrueba().Pilotos,
		}},
		{"piloto sin opción", DatosResolucion{
			Muestras: []MuestraPosicion{{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 81, Position: 1}},
		}},
		{"nombre de piloto ambiguo", DatosResolucion{
			Muestras: []MuestraPosicion{{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 1, Position: 1}},
			Pilotos: []Piloto{
				{DriverNumber: 1, FullName: "Piloto", NameAcronym: "VER"},
				{DriverNumber: 4, FullName: "Piloto", NameAcronym: "NOR"},
			},
		}},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			mercado := m
			if caso.nombre == "nombre de piloto ambiguo" {
				mercado.Opciones = []string{"Piloto", "Lando Norris"}
			}
			if _, err := Resolver(mercado, caso.datos); !errors.Is(err, ErrDatosInsuficientes) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	m.Tipo = "texto_libre"
	if _, err := Resolver(m, datosPrueba()); !errors.Is(err, ErrTipoNoAdmitido) {
		t.Fatalf("tipo no admitido produjo %v", err)
	}
}

func TestResolverRechazaMuestrasConTimestampsIncompatibles(t *testing.T) {
	m := mercadoPrueba()
	datos := datosPrueba()
	datos.Muestras[2].Timestamp = instante.Add(3 * time.Second)
	datos.Muestras[2].LeidoEn = instante.Add(3 * time.Second)

	if _, err := Resolver(m, datos); !errors.Is(err, ErrMuestrasIncompatibles) {
		t.Fatalf("error = %v, se esperaba %v", err, ErrMuestrasIncompatibles)
	}
}

func TestResolverUsaInstantaneaCompletaCoherenteMasReciente(t *testing.T) {
	m := mercadoPrueba()
	datos := datosPrueba()
	// Estas filas posteriores son parciales y no se mezclan con la
	// instantánea común completa de t+2s.
	datos.Muestras = append(datos.Muestras,
		MuestraPosicion{
			Timestamp: instante.Add(4 * time.Second), LeidoEn: instante.Add(4 * time.Second),
			DriverNumber: 1, Position: 2,
		},
	)

	resultado, err := Resolver(m, datos)
	if err != nil {
		t.Fatal(err)
	}
	if resultado != "Max Verstappen" {
		t.Fatalf("resultado %q", resultado)
	}
}

func TestResolverRechazaEmpateEnInstantaneaCoherente(t *testing.T) {
	m := mercadoPrueba()
	datos := DatosResolucion{
		Muestras: []MuestraPosicion{
			{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 1, Position: 1},
			{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 4, Position: 1},
		},
		Pilotos: datosPrueba().Pilotos,
	}

	if _, err := Resolver(m, datos); !errors.Is(err, ErrClasificacionAmbigua) {
		t.Fatalf("error = %v, se esperaba %v", err, ErrClasificacionAmbigua)
	}
}

type storeFalso struct {
	mu           sync.Mutex
	mercado      Mercado
	datos        DatosResolucion
	contextos    []ContextoApuesta
	retryEn      time.Time
	reclamado    bool
	finalizado   bool
	errorUltimo  string
	fallarGuardar bool
	resumen       LiquidacionMercado
}

func (s *storeFalso) CerrarVencidos(context.Context, time.Time, time.Duration) ([]Mercado, error) {
	return nil, nil
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
	mu            sync.Mutex
	efectos       map[string]float64
	llamadas      int
	aplicaciones  int
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
	w := NuevoWorker(s, p, time.Minute, nil)
	w.ahora = func() time.Time { return instante.Add(time.Minute) }
	return w
}

func TestLiquidacionMarcaGanadorYPerdedor(t *testing.T) {
	s := &storeFalso{
		mercado: mercadoPrueba(), datos: datosPrueba(),
		contextos: []ContextoApuesta{
			{ApuestaID: "bet-won", Opcion: "Max Verstappen", Monto: 100},
			{ApuestaID: "bet-lost", Opcion: "Lando Norris", Monto: 20},
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
	if s.resumen.Resultado != "Max Verstappen" || s.resumen.PuntosOtorgados != 60 {
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

func TestResolverRechazadoNoSolicitaLiquidacionAFinanzas(t *testing.T) {
	s := &storeFalso{
		mercado: mercadoPrueba(),
		datos: DatosResolucion{
			Muestras: []MuestraPosicion{
				{Timestamp: instante.Add(time.Second), LeidoEn: instante.Add(time.Second), DriverNumber: 1, Position: 1},
				{Timestamp: instante.Add(2 * time.Second), LeidoEn: instante.Add(2 * time.Second), DriverNumber: 4, Position: 2},
			},
			Pilotos: datosPrueba().Pilotos,
		},
		contextos: []ContextoApuesta{{ApuestaID: "bet-won", Opcion: "Max Verstappen", Monto: 100}},
	}
	p := &pagosFalso{efectos: map[string]float64{}}
	w := NuevoWorkerPrueba(s, p)
	if err := w.Procesar(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.finalizado || p.llamadas != 0 || s.errorUltimo == "" {
		t.Fatalf("no debe liquidar ante timestamps incompatibles: mercado=%+v llamadas Pagos=%d", s, p.llamadas)
	}
	if !strings.Contains(s.errorUltimo, "timestamp común") {
		t.Fatalf("diagnóstico poco útil: %q", s.errorUltimo)
	}
}

func TestReintentoTrasFalloMongoNoDuplicaEfectosDePagos(t *testing.T) {
	s := &storeFalso{
		mercado: mercadoPrueba(), datos: datosPrueba(),
		contextos: []ContextoApuesta{{ApuestaID: "bet-won", Opcion: "Max Verstappen", Monto: 100}},
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
		contextos: []ContextoApuesta{{ApuestaID: "bet-won", Opcion: "Max Verstappen", Monto: 100}},
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
