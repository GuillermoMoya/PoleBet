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

	// HU2-11: campos del modo torneo. Son aditivos y opcionales: un contexto
	// sin ellos (apuesta individual de HU-06/07) se liquida como siempre en
	// tokens contra Pagos. Si Modo == "torneo", la apuesta se liquida en
	// fichas (Redis) y no toca Pagos.
	Modo          string  // "individual" (default) o "torneo"
	Multiplicador float64 // multiplicador de la opcion, para pagar fichas
	EventoID      string  // evento del torneo al que pertenece la apuesta
}

// ModoTorneo marca una apuesta que se liquida en fichas del torneo (HU2-11).
const ModoTorneo = "torneo"

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

// LiquidadorTorneo liquida una apuesta de torneo en fichas (Redis) en lugar de
// tokens. Lo implementa fichas.Puente (HU2-11). Es opcional: si el Worker no
// lo tiene, un contexto de torneo se registra como error y queda reintentable,
// sin tocar Pagos (nunca se liquida una apuesta de torneo en tokens).
//
// Liquidar escribe el resultado de forma idempotente y devuelve las fichas del
// jugador tras la liquidacion y su nuevo puesto, para difundir el resultado
// (RF-17) y el ranking (RF-18). Difundir envia el ranking del evento.
type LiquidadorTorneo interface {
	Liquidar(ctx context.Context, eventoID, apuestaID string, gano bool, multiplicador float64) (ResultadoTorneo, error)
	Difundir(ctx context.Context, eventoID string)
}

// ResultadoTorneo es lo que el Worker necesita saber tras liquidar una apuesta
// de torneo para notificar al jugador (RF-17).
type ResultadoTorneo struct {
	FichasGanadas int64 // fichas pagadas por esta apuesta (0 si perdio)
	FichasTotal   int64 // saldo de fichas del jugador tras liquidar
	Puesto        int   // puesto del jugador en el ranking tras liquidar
}

// Notificador es lo que el Worker usa del hub de WebSocket. *ws.Hub lo
// cumple; en las pruebas se reemplaza por un hub falso.
type Notificador interface {
	Difundir(ws.Mensaje) error
	EnviarA(string, ws.Mensaje) error
}

// ResultadoApuesta es el payload de resultado_apuesta (HU-08), que se envia
// solo al dueño de la apuesta al liquidarla. Los tokens apostados no se
// devuelven, por eso no lleva saldo: la liquidacion no lo cambia.
type ResultadoApuesta struct {
	MercadoID       string  `json:"mercado_id"`
	ApuestaID       string  `json:"apuesta_id"`
	Opcion          string  `json:"opcion"`
	OpcionGanadora  string  `json:"opcion_ganadora"`
	Gano            bool    `json:"gano"`
	PuntosObtenidos float64 `json:"puntos_obtenidos"`
	PuntajeTotal    float64 `json:"puntaje_total"` // points_total de Pagos

	// HU2-11: campos del modo torneo. Se incluyen solo si Modo == "torneo",
	// para que el front muestre fichas ganadas y el nuevo puesto (RF-17). En
	// individual no aparecen (omitempty) y se mantienen puntos/saldo de HU-08.
	Modo          string `json:"modo,omitempty"`
	FichasGanadas int64  `json:"fichas_ganadas,omitempty"`
	FichasTotal   int64  `json:"fichas_total,omitempty"`
	Puesto        int    `json:"puesto,omitempty"`
}

type Worker struct {
	store     Store
	pagos     Pagos
	torneo    LiquidadorTorneo // HU2-11: opcional, liquida apuestas de torneo en fichas
	intervalo time.Duration
	hub       Notificador
	ahora     func() time.Time
}

func NuevoWorker(store Store, pagos Pagos, hub Notificador) *Worker {
	return &Worker{
		store:     store,
		pagos:     pagos,
		intervalo: 2 * time.Second,
		hub:       hub,
		ahora:     time.Now,
	}
}

// ConLiquidadorTorneo habilita la liquidacion de apuestas de torneo en fichas
// (HU2-11). Se llama despues de NuevoWorker, antes de Run. Devuelve el mismo
// Worker para poder encadenar.
func (w *Worker) ConLiquidadorTorneo(t LiquidadorTorneo) *Worker {
	w.torneo = t
	return w
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
		// La coherencia de puntos solo aplica a individuales: en torneo no hay
		// puntos (se pagan fichas en Redis), por eso se omite la validacion.
		if contexto.Liquidada && contexto.Modo != ModoTorneo {
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
	hayTorneo := false
	for _, contexto := range contextos {
		gano := contexto.Opcion == resultado
		// HU2-11: las apuestas de torneo se liquidan en fichas (Redis), nunca
		// en tokens. Se desvian aqui; el resto sigue el flujo individual.
		if contexto.Modo == ModoTorneo {
			hayTorneo = true
			if err := w.liquidarTorneo(ctx, mercado, contexto, resultado, gano); err != nil {
				return liberarConError(err)
			}
			continue
		}
		if contexto.Liquidada {
			totalPuntos += contexto.Puntos
			continue
		}
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
		// HU-08: solo despues de guardar, para que un reintento (que salta las
		// apuestas ya liquidadas) no lo reenvie.
		w.enviarA(contexto.UsuarioID, ws.TipoResultadoApuesta, ResultadoApuesta{
			MercadoID:       mercado.ID,
			ApuestaID:       contexto.ApuestaID,
			Opcion:          contexto.Opcion,
			OpcionGanadora:  resultado,
			Gano:            gano,
			PuntosObtenidos: puntos,
			PuntajeTotal:    respuesta.PuntosTotales,
		})
		totalPuntos += respuesta.PuntosOtorgados
	}

	// HU2-11: tras liquidar las apuestas de torneo del mercado, difundir el
	// ranking una sola vez con el estado final (RF-18, < 1 s).
	if hayTorneo && w.torneo != nil {
		if eventoID := eventoDe(contextos); eventoID != "" {
			w.torneo.Difundir(ctx, eventoID)
		}
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

// liquidarTorneo liquida una apuesta de torneo en fichas (HU2-11). Requiere un
// LiquidadorTorneo configurado; sin el, la apuesta queda reintentable (nunca
// se liquida un torneo en tokens). Marca el contexto como liquidado en MongoDB
// (con puntos=0, porque el torneo no usa puntos) para que un reintento no la
// reprocese, y notifica al jugador sus fichas y su nuevo puesto (RF-17).
func (w *Worker) liquidarTorneo(ctx context.Context, mercado Mercado, contexto ContextoApuesta, resultado string, gano bool) error {
	if w.torneo == nil {
		return fmt.Errorf("apuesta de torneo %s sin liquidador de fichas configurado", contexto.ApuestaID)
	}
	if contexto.EventoID == "" {
		return fmt.Errorf("apuesta de torneo %s sin evento asociado", contexto.ApuestaID)
	}
	res, err := w.torneo.Liquidar(ctx, contexto.EventoID, contexto.ApuestaID, gano, contexto.Multiplicador)
	if err != nil {
		return fmt.Errorf("liquidar apuesta de torneo en fichas: %w", err)
	}
	// Guardar en MongoDB marca la apuesta como liquidada. Puntos=0: en torneo
	// no hay puntos. El almacen de fichas ya es idempotente, asi que un
	// reintento no paga doble aunque este guardado falle y se repita.
	if err := w.store.GuardarResultadoApuesta(ctx, mercado.ID, contexto, gano, 0); err != nil {
		return fmt.Errorf("guardar resultado de apuesta de torneo: %w", err)
	}
	// RF-17: en torneo se envian fichas ganadas o perdidas y el nuevo puesto.
	w.enviarA(contexto.UsuarioID, ws.TipoResultadoApuesta, ResultadoApuesta{
		MercadoID:      mercado.ID,
		ApuestaID:      contexto.ApuestaID,
		Opcion:         contexto.Opcion,
		OpcionGanadora: resultado,
		Gano:           gano,
		Modo:           ModoTorneo,
		FichasGanadas:  res.FichasGanadas,
		FichasTotal:    res.FichasTotal,
		Puesto:         res.Puesto,
	})
	return nil
}

// eventoDe devuelve el evento del primer contexto de torneo. Todas las
// apuestas de un mercado pertenecen al mismo evento.
func eventoDe(contextos []ContextoApuesta) string {
	for _, c := range contextos {
		if c.Modo == ModoTorneo && c.EventoID != "" {
			return c.EventoID
		}
	}
	return ""
}

func (w *Worker) difundir(tipo string, payload any) {
	if w.hub == nil {
		return
	}
	if err := w.hub.Difundir(ws.Mensaje{Type: tipo, Payload: payload}); err != nil {
		log.Printf("HU-07: no se pudo difundir %s: %v", tipo, err)
	}
}

// enviarA notifica a un solo usuario. Si falla (hub detenido, sin user_id)
// solo se registra: la apuesta ya quedo liquidada y no se reintenta por esto.
// Si el usuario no esta conectado el hub descarta el mensaje sin error.
func (w *Worker) enviarA(userID, tipo string, payload any) {
	if w.hub == nil {
		return
	}
	if err := w.hub.EnviarA(userID, ws.Mensaje{Type: tipo, Payload: payload}); err != nil {
		log.Printf("HU-08: no se pudo enviar %s a user_id=%q: %v", tipo, userID, err)
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
