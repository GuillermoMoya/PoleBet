// Package liquidacion orquesta la resolución limitada de HU-07.
package liquidacion

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/arquisoft/motor/internal/apuestas"
	"github.com/arquisoft/motor/internal/ws"
)

const TipoPilotoPrimero = "driver_first_position"

var (
	ErrTipoNoAdmitido       = errors.New("tipo de mercado no admitido")
	ErrDatosInsuficientes   = errors.New("datos de carrera insuficientes")
	ErrMuestrasIncompatibles = fmt.Errorf("%w: no existe una instantánea completa con timestamp común para todos los pilotos", ErrDatosInsuficientes)
	ErrClasificacionAmbigua = fmt.Errorf("%w: la instantánea contiene posiciones duplicadas o un líder ambiguo", ErrDatosInsuficientes)
	ErrContextoInvalido     = errors.New("contexto de apuesta inválido")
)

type Mercado struct {
	ID           string
	CarreraID    string
	Tipo         string
	Estado       string
	Opciones     []string
	CierraEn     time.Time
	ObservaHasta time.Time
}

type MuestraPosicion struct {
	Timestamp    time.Time
	LeidoEn      time.Time
	DriverNumber int
	Position     int
	DriverName   string
}

type Piloto struct {
	DriverNumber int
	FullName     string
	NameAcronym  string
}

type DatosResolucion struct {
	Muestras []MuestraPosicion
	Pilotos  []Piloto
}

type ContextoApuesta struct {
	ApuestaID string
	UsuarioID string
	Opcion    string
	Monto     int64
	Liquidada bool
	Gano      *bool
	Puntos    float64
}

type LiquidacionMercado struct {
	Resultado       string
	PuntosOtorgados float64
}

type Store interface {
	CerrarVencidos(context.Context, time.Time, time.Duration) ([]Mercado, error)
	MercadosVencidos(context.Context, time.Time) ([]Mercado, error)
	Reclamar(context.Context, string, time.Time, time.Duration) (string, bool, error)
	Datos(context.Context, Mercado) (DatosResolucion, error)
	Contextos(context.Context, string) ([]ContextoApuesta, error)
	GuardarResultadoApuesta(context.Context, string, ContextoApuesta, bool, float64) error
	Finalizar(context.Context, Mercado, string, LiquidacionMercado, time.Time) error
	Reintentar(context.Context, string, string, string, time.Time) error
}

type Pagos interface {
	Liquidar(context.Context, apuestas.Liquidacion) (apuestas.ResultadoLiquidacion, error)
}

type Worker struct {
	store       Store
	pagos       Pagos
	observacion time.Duration
	intervalo   time.Duration
	hub         *ws.Hub
	ahora       func() time.Time
}

func NuevoWorker(store Store, pagos Pagos, observacion time.Duration, hub *ws.Hub) *Worker {
	return &Worker{
		store:       store,
		pagos:       pagos,
		observacion: observacion,
		intervalo:   2 * time.Second,
		hub:         hub,
		ahora:       time.Now,
	}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.intervalo)
	defer ticker.Stop()
	for {
		if err := w.Procesar(ctx); err != nil && ctx.Err() == nil {
			log.Printf("HU-07: error al procesar mercados: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) Procesar(ctx context.Context) error {
	ahora := w.ahora().UTC()
	cerrados, err := w.store.CerrarVencidos(ctx, ahora, w.observacion)
	if err != nil {
		return fmt.Errorf("cerrar mercados vencidos: %w", err)
	}
	for _, mercado := range cerrados {
		w.difundir(ws.TipoMercadoCerrado, map[string]any{
			"mercado_id": mercado.ID,
			"estado":     "cerrado",
			"observa_hasta": mercado.ObservaHasta,
		})
	}

	vencidos, err := w.store.MercadosVencidos(ctx, ahora)
	if err != nil {
		return fmt.Errorf("buscar mercados listos: %w", err)
	}
	for _, mercado := range vencidos {
		if err := w.procesarMercado(ctx, mercado, ahora); err != nil {
			log.Printf("HU-07: mercado %s queda reintentable: %v", mercado.ID, err)
		}
	}
	return nil
}

func (w *Worker) procesarMercado(ctx context.Context, mercado Mercado, ahora time.Time) error {
	token, reclamado, err := w.store.Reclamar(ctx, mercado.ID, ahora, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("reclamar mercado: %w", err)
	}
	if !reclamado {
		return nil
	}
	liberarConError := func(causa error) error {
		msg := causa.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if err := w.store.Reintentar(ctx, mercado.ID, token, msg, w.ahora().UTC().Add(w.intervalo)); err != nil {
			return fmt.Errorf("%v; registrar reintento: %w", causa, err)
		}
		return causa
	}

	datos, err := w.store.Datos(ctx, mercado)
	if err != nil {
		return liberarConError(fmt.Errorf("leer datos: %w", err))
	}
	resultado, err := Resolver(mercado, datos)
	if err != nil {
		return liberarConError(err)
	}

	contextos, err := w.store.Contextos(ctx, mercado.ID)
	if err != nil {
		return liberarConError(fmt.Errorf("leer contextos: %w", err))
	}
	for _, contexto := range contextos {
		if contexto.ApuestaID == "" || contexto.Monto <= 0 || !contiene(mercado.Opciones, contexto.Opcion) {
			return liberarConError(ErrContextoInvalido)
		}
		if contexto.Liquidada {
			gano := contexto.Opcion == resultado
			puntos := 0.0
			if gano {
				puntos = puntosPorApuesta(contexto.Monto)
			}
			if contexto.Gano == nil || *contexto.Gano != gano || !samePoints(contexto.Puntos, puntos) {
				return liberarConError(errors.New("contexto liquidado tiene puntos inconsistentes"))
			}
		}
	}

	totalPuntos := 0.0
	for _, contexto := range contextos {
		if contexto.Liquidada {
			totalPuntos += contexto.Puntos
			continue
		}
		gano := contexto.Opcion == resultado
		puntos := 0.0
		estado := "lost"
		if gano {
			estado = "won"
			puntos = puntosPorApuesta(contexto.Monto)
		}
		respuesta, err := w.pagos.Liquidar(ctx, apuestas.Liquidacion{
			BetID:          contexto.ApuestaID,
			Resultado:      estado,
			Puntos:         puntos,
			IdempotencyKey: "bet-settlement:" + contexto.ApuestaID,
		})
		if err != nil {
			return liberarConError(fmt.Errorf("liquidar apuesta en Pagos: %w", err))
		}
		if respuesta.BetID != contexto.ApuestaID || respuesta.Estado != estado ||
			!samePoints(respuesta.PuntosOtorgados, puntos) {
			return liberarConError(errors.New("respuesta de Pagos incompatible con la liquidación solicitada"))
		}
		if err := w.store.GuardarResultadoApuesta(ctx, mercado.ID, contexto, gano, puntos); err != nil {
			return liberarConError(fmt.Errorf("guardar resultado de apuesta: %w", err))
		}
		totalPuntos += respuesta.PuntosOtorgados
	}

	resumen := LiquidacionMercado{Resultado: resultado, PuntosOtorgados: totalPuntos}
	if err := w.store.Finalizar(ctx, mercado, token, resumen, w.ahora().UTC()); err != nil {
		return liberarConError(fmt.Errorf("finalizar mercado en MongoDB: %w", err))
	}
	w.difundir(ws.TipoMercadoLiquidado, map[string]any{
		"mercado_id": mercado.ID,
		"resultado": resultado,
		"puntos_otorgados": totalPuntos,
	})
	return nil
}

func (w *Worker) difundir(tipo string, payload any) {
	if w.hub != nil {
		w.hub.Difundir(ws.Mensaje{Type: tipo, Payload: payload})
	}
}

func Resolver(mercado Mercado, datos DatosResolucion) (string, error) {
	if mercado.Tipo != TipoPilotoPrimero {
		return "", ErrTipoNoAdmitido
	}
	if mercado.Estado != "cerrado" || len(mercado.Opciones) == 0 ||
		mercado.CierraEn.IsZero() || !mercado.ObservaHasta.After(mercado.CierraEn) {
		return "", ErrDatosInsuficientes
	}
	aliases := make(map[int][]string)
	roster := make(map[int]bool)
	for _, piloto := range datos.Pilotos {
		if piloto.DriverNumber <= 0 {
			continue
		}
		roster[piloto.DriverNumber] = true
		aliases[piloto.DriverNumber] = append(
			aliases[piloto.DriverNumber],
			strings.TrimSpace(piloto.FullName),
			strings.TrimSpace(piloto.NameAcronym),
		)
	}

	// MongoDB conserva los timestamps con precisión de milisegundos. Una
	// clasificación solo combina filas del mismo milisegundo, nunca la
	// última fila independiente de cada piloto.
	snapshots := make(map[int64]map[int]int)
	snapshotConflictivo := make(map[int64]bool)
	nombrePorPiloto := make(map[int][]string)
	for _, muestra := range datos.Muestras {
		if muestra.Timestamp.Before(mercado.CierraEn) || muestra.Timestamp.After(mercado.ObservaHasta) {
			continue
		}
		if muestra.LeidoEn.Before(mercado.CierraEn) || muestra.LeidoEn.After(mercado.ObservaHasta) {
			continue
		}
		if muestra.DriverNumber <= 0 || muestra.Position <= 0 || muestra.Timestamp.IsZero() || muestra.LeidoEn.IsZero() {
			continue
		}
		if !roster[muestra.DriverNumber] {
			return "", ErrDatosInsuficientes
		}
		if nombre := strings.TrimSpace(muestra.DriverName); nombre != "" {
			nombrePorPiloto[muestra.DriverNumber] = append(nombrePorPiloto[muestra.DriverNumber], nombre)
		}
		milisegundo := muestra.Timestamp.UTC().UnixMilli()
		if snapshots[milisegundo] == nil {
			snapshots[milisegundo] = make(map[int]int)
		}
		if previa, existe := snapshots[milisegundo][muestra.DriverNumber]; existe && previa != muestra.Position {
			snapshotConflictivo[milisegundo] = true
		} else {
			snapshots[milisegundo][muestra.DriverNumber] = muestra.Position
		}
	}

	if len(roster) == 0 {
		return "", ErrDatosInsuficientes
	}
	var timestampCoherente int64
	hayInstantaneaCompleta := false
	for timestamp, posiciones := range snapshots {
		if len(posiciones) == len(roster) && (!hayInstantaneaCompleta || timestamp > timestampCoherente) {
			timestampCoherente = timestamp
			hayInstantaneaCompleta = true
		}
	}
	if !hayInstantaneaCompleta {
		return "", ErrMuestrasIncompatibles
	}
	if snapshotConflictivo[timestampCoherente] {
		return "", ErrClasificacionAmbigua
	}
	posiciones := snapshots[timestampCoherente]
	pilotoPorPosicion := make(map[int]int, len(posiciones))
	for piloto, posicion := range posiciones {
		if _, existe := pilotoPorPosicion[posicion]; existe {
			return "", ErrClasificacionAmbigua
		}
		pilotoPorPosicion[posicion] = piloto
	}
	numero, hayLider := pilotoPorPosicion[1]
	if !hayLider {
		return "", ErrClasificacionAmbigua
	}
	var opciones []string
	for _, opcion := range mercado.Opciones {
		encontrados := make(map[int]bool)
		for driverNumber, nombres := range aliases {
			for _, nombre := range nombres {
				if nombre != "" && strings.EqualFold(strings.TrimSpace(opcion), nombre) {
					encontrados[driverNumber] = true
				}
			}
		}
		for driverNumber, nombres := range nombrePorPiloto {
			for _, nombre := range nombres {
				if nombre != "" && strings.EqualFold(strings.TrimSpace(opcion), nombre) {
					encontrados[driverNumber] = true
				}
			}
		}
		if len(encontrados) > 1 {
			return "", ErrDatosInsuficientes
		}
		if encontrados[numero] {
			opciones = append(opciones, opcion)
		}
	}
	if len(opciones) != 1 {
		return "", ErrDatosInsuficientes
	}
	return opciones[0], nil
}

func puntosPorApuesta(monto int64) float64 {
	return 10 + float64(monto)*0.5
}

func samePoints(a, b float64) bool {
	return a == b
}

func contiene(opciones []string, opcion string) bool {
	for _, candidata := range opciones {
		if candidata == opcion {
			return true
		}
	}
	return false
}
