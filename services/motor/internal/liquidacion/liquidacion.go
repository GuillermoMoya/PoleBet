// Package liquidacion implementa HU-07: liquidar las apuestas de un mercado
// cuando termina su ventana de observacion.
//
// Reparto de responsabilidades:
//
//   - El paquete mercados (HU-11) abre y CIERRA los mercados. Este paquete
//     nunca cierra nada: su Worker consulta MongoDB cada 2 s por mercados ya
//     "cerrado" con observa_hasta vencido y los liquida.
//   - Resolver decide la opcion ganadora (funcion pura, sin BD).
//   - Pagos (REST) suma los puntos y registra el movimiento; MongoDB guarda el
//     resultado de cada apuesta y del mercado. Un lease en MongoDB evita que
//     dos workers liquiden el mismo mercado, y las claves de idempotencia
//     (bet-settlement:{id}) permiten reintentar sin duplicar efectos.
//
// Regla del unico tipo de mercado (mercados.TipoCambioPosicion), Si/No:
//
//		¿Habra algun cambio de posicion durante la ventana de observacion?
//
//	  - Gana "Sí" si la reproduccion de la carrera emitio al menos un evento de
//	    posicion con emitido_en dentro de [cierra_en, observa_hasta] (ambos
//	    limites incluidos). Cada fila de posicion de OpenF1 es un cambio de
//	    posicion de un piloto; ver ingest/replay.go.
//	  - Gana "No" si no hubo ninguno Y la reproduccion estuvo viva durante toda
//	    la ventana (latido en la coleccion reproducciones).
//	  - Si no hubo eventos y no hay prueba de que la reproduccion corria, no se
//	    sabe quien gano: el mercado queda cerrado y reintentable, con el
//	    diagnostico en liquidacion_error.
//
// Por que emitido_en y no timestamp: la carrera es pasada (OpenF1 se
// descarga una vez) y timestamp es la hora historica del evento, que nunca
// cae dentro de la ventana del mercado medida con el reloj del servidor.
package liquidacion

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/arquisoft/motor/internal/apuestas"
	"github.com/arquisoft/motor/internal/mercados"
	"github.com/arquisoft/motor/internal/ws"
)

var (
	ErrTipoNoAdmitido     = errors.New("tipo de mercado no admitido")
	ErrDatosInsuficientes = errors.New("datos de carrera insuficientes")
	// ErrSinReproduccion: no hubo eventos en la ventana, pero tampoco hay
	// prueba de que la reproduccion estuviera corriendo; no se puede dar "No".
	ErrSinReproduccion  = fmt.Errorf("%w: no se emitieron eventos de posicion en la ventana y no hay constancia de que la reproduccion estuviera corriendo durante toda la ventana", ErrDatosInsuficientes)
	ErrContextoInvalido = errors.New("contexto de apuesta inválido")
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

// DatosResolucion es lo que Resolver necesita saber de la carrera durante la
// ventana [cierra_en, observa_hasta] del mercado (lo arma Store.Datos).
type DatosResolucion struct {
	// HuboEventoPosicion: se emitio al menos un evento de posicion de la
	// carrera del mercado con emitido_en dentro de la ventana.
	HuboEventoPosicion bool
	// ReproduccionCubre: la reproduccion de esa carrera ya corria en cierra_en
	// (iniciada_en <= cierra_en) y seguia viva en observa_hasta
	// (latido_en >= observa_hasta), sin reiniciarse en medio.
	ReproduccionCubre bool
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

// Store es el acceso a MongoDB del liquidador. No cierra mercados: de abierto a
// cerrado se encarga solo el paquete mercados (HU-11); aqui se leen los que ya
// estan cerrados y cuya observacion termino.
type Store interface {
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
	store     Store
	pagos     Pagos
	intervalo time.Duration
	hub       *ws.Hub
	ahora     func() time.Time
}

func NuevoWorker(store Store, pagos Pagos, hub *ws.Hub) *Worker {
	return &Worker{
		store:     store,
		pagos:     pagos,
		intervalo: 2 * time.Second,
		hub:       hub,
		ahora:     time.Now,
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

// Procesar liquida los mercados que ya estan cerrados y cuya observacion
// termino. El cierre (y su difusion mercado_cerrado) es de HU-11.
func (w *Worker) Procesar(ctx context.Context) error {
	ahora := w.ahora().UTC()
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
		"mercado_id":       mercado.ID,
		"resultado":        resultado,
		"puntos_otorgados": totalPuntos,
	})
	return nil
}

func (w *Worker) difundir(tipo string, payload any) {
	if w.hub != nil {
		w.hub.Difundir(ws.Mensaje{Type: tipo, Payload: payload})
	}
}

// Resolver decide la opcion ganadora de un mercado ya cerrado. Es una funcion
// pura: todo lo que sabe de la carrera viene en datos (ver DatosResolucion y
// la regla en el comentario del paquete). Se guia SOLO por mercado.Tipo,
// nunca por el texto de la pregunta.
//
// Devuelve un error envuelto en ErrDatosInsuficientes cuando aun no se puede
// decidir; el Worker lo registra y reintenta, sin tocar las apuestas.
func Resolver(mercado Mercado, datos DatosResolucion) (string, error) {
	if mercado.Tipo != mercados.TipoCambioPosicion {
		return "", ErrTipoNoAdmitido
	}
	if mercado.Estado != mercados.EstadoCerrado ||
		mercado.CierraEn.IsZero() || !mercado.ObservaHasta.After(mercado.CierraEn) {
		return "", ErrDatosInsuficientes
	}
	// El motor solo abre este tipo con exactamente ["Sí", "No"]; si un
	// documento viejo o editado a mano no las tiene, no se inventa un ganador.
	if !contiene(mercado.Opciones, mercados.OpcionSi) || !contiene(mercado.Opciones, mercados.OpcionNo) {
		return "", fmt.Errorf("%w: el mercado no tiene las opciones %q y %q", ErrDatosInsuficientes, mercados.OpcionSi, mercados.OpcionNo)
	}
	if datos.HuboEventoPosicion {
		return mercados.OpcionSi, nil
	}
	if datos.ReproduccionCubre {
		return mercados.OpcionNo, nil
	}
	return "", ErrSinReproduccion
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
