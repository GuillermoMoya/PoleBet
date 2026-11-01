// Package fichas implementa HU2-11: las fichas del torneo y el ranking en
// vivo. Es el "almacen de fichas" del que el Motor es dueño (RNF-18): las
// fichas viven en Redis, no en PostgreSQL (alli solo quedan chips_final al
// cerrar el evento, HU2-12).
//
// Fronteras con el resto del equipo (acordadas en el reparto del Prototipo 2):
//
//   - HU2-06 (Michael, Pagos): al largar, POST /internal/events/{id}/start
//     responde la lista de jugadores inscritos e initial_chips. El Motor usa
//     ese dato para inicializar las fichas de cada jugador (InicializarTorneo).
//   - HU2-09 (Michael, Motor): al apostar en modo torneo, descuenta fichas con
//     ReservarApuesta antes de registrar la apuesta en Pagos.
//   - HU2-11 (este paquete): al liquidar una apuesta de torneo, acredita
//     fichas x multiplicador (Liquidar) y difunde el ranking.
//   - HU2-12 (reparto): lee el estado final de las fichas y lo lleva a
//     event_entries (chips_final, chips_wagered) en Pagos.
//
// Regla de oro de idempotencia (RNF-12): las fichas NUNCA se suman a ciegas.
// Cada apuesta escribe su resultado una sola vez (clave por apuesta) y el
// total del jugador se RECALCULA a partir de su saldo inicial, sus reservas y
// los resultados de sus apuestas. Repetir una liquidacion o reservar la misma
// apuesta deja las fichas igual. Esto hace el componente seguro ante
// reintentos, reinicios del Motor y dobles entregas del WebSocket.
package fichas

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Errores de negocio del almacen. El llamador (HU2-09, el worker de
// liquidacion) decide como traducirlos.
var (
	// ErrTorneoNoActivo: el evento no tiene un torneo inicializado en Redis.
	ErrTorneoNoActivo = errors.New("el torneo no esta activo")
	// ErrJugadorNoInscrito: el usuario no recibio fichas en la largada.
	ErrJugadorNoInscrito = errors.New("el jugador no esta inscrito en el torneo")
	// ErrFichasInsuficientes: la apuesta pide mas fichas de las disponibles.
	// "Quien llega a 0 fichas ya no puede apostar" (reglas del torneo).
	ErrFichasInsuficientes = errors.New("fichas insuficientes")
	// ErrPeticionInvalida: datos de entrada vacios o fuera de rango.
	ErrPeticionInvalida = errors.New("peticion de fichas invalida")
	// ErrReservaIncompatible: la misma apuesta ya se reservo con otro monto u
	// opcion. Protege la idempotencia de contenido (no solo de clave).
	ErrReservaIncompatible = errors.New("la apuesta ya existe con otro contenido")
)

// Estado resume las fichas de un jugador en el torneo. Es lo que el ranking
// y la barra de avance (RF-18) necesitan de cada inscrito.
type Estado struct {
	UsuarioID string
	// Fichas es el saldo actual: initial - apostado + ganado (recalculado).
	Fichas int64
	// Apostado es el total de fichas comprometidas en apuestas (para la barra
	// de avance hacia el minimo del 50 %). No baja aunque la apuesta pierda.
	Apostado int64
}

// ResultadoApuesta es el efecto de liquidar una apuesta de torneo. Se escribe
// una sola vez por apuesta (idempotencia) y de ahi se recalcula el total.
type ResultadoApuesta struct {
	Gano          bool
	FichasPagadas int64 // monto x multiplicador si gano; 0 si perdio
}

// Reserva es lo que se guarda al apostar en modo torneo (HU2-09): cuanto se
// comprometio y con que opcion/multiplicador, para poder liquidar luego.
type Reserva struct {
	ApuestaID   string
	UsuarioID   string
	Monto       int64
	Opcion      string
	Multiplic   float64
	ReservadaEn time.Time
}

// Store es el acceso al almacen persistente (Redis en produccion, un doble en
// las pruebas). Trabaja por evento y es la unica parte que conoce las claves.
//
// Todas las operaciones deben ser seguras ante reintentos: escribir el mismo
// dato dos veces no cambia el resultado.
type Store interface {
	// InicializarJugadores crea el saldo inicial de cada jugador si aun no
	// existe. Repetirla no reinicia a quien ya tiene fichas (idempotente).
	InicializarJugadores(ctx context.Context, eventoID string, jugadores []string, fichasIniciales int64) error
	// TorneoActivo indica si el evento ya fue inicializado.
	TorneoActivo(ctx context.Context, eventoID string) (bool, error)
	// GuardarReserva escribe la reserva de una apuesta solo si no existia.
	// Devuelve nueva=false si ya estaba (con el mismo contenido).
	GuardarReserva(ctx context.Context, eventoID string, r Reserva) (nueva bool, err error)
	// Reserva devuelve una reserva por su id de apuesta; ok=false si no esta.
	Reserva(ctx context.Context, eventoID, apuestaID string) (Reserva, bool, error)
	// GuardarResultado escribe el resultado de una apuesta solo si no existia.
	// Devuelve nueva=false si ya estaba.
	GuardarResultado(ctx context.Context, eventoID, apuestaID string, res ResultadoApuesta) (nueva bool, err error)
	// Recalcular vuelve a calcular y persiste el saldo de fichas del jugador a
	// partir de su saldo inicial, sus reservas y los resultados escritos. Es
	// la operacion que garantiza que las fichas no se "sumen" dos veces.
	Recalcular(ctx context.Context, eventoID, usuarioID string) (Estado, error)
	// Estados lee el estado de todos los inscritos del evento (para el ranking).
	Estados(ctx context.Context, eventoID string) ([]Estado, error)
	// Limpiar borra todas las claves del evento (lo usa HU2-12 al cerrar).
	Limpiar(ctx context.Context, eventoID string) error
}

// Almacen es la logica de fichas de HU2-11. No conoce Redis: depende de Store.
type Almacen struct {
	store Store
	ahora func() time.Time
}

func NuevoAlmacen(store Store) *Almacen {
	return &Almacen{store: store, ahora: time.Now}
}

// InicializarTorneo da a cada jugador sus fichas iniciales al largar el
// torneo (RF-07). Lo llama el flujo de largada con los datos que devuelve
// POST /internal/events/{id}/start de Pagos (HU2-06). Es idempotente: si el
// torneo ya se inicializo, no reinicia a nadie.
func (a *Almacen) InicializarTorneo(ctx context.Context, eventoID string, jugadores []string, fichasIniciales int64) error {
	if eventoID == "" || fichasIniciales <= 0 {
		return ErrPeticionInvalida
	}
	// Un torneo sin jugadores no es un error: la largada pudo cancelarlo por
	// pocos inscritos (HU2-06). Simplemente no hay fichas que crear.
	if len(jugadores) == 0 {
		return nil
	}
	for _, j := range jugadores {
		if j == "" {
			return ErrPeticionInvalida
		}
	}
	return a.store.InicializarJugadores(ctx, eventoID, jugadores, fichasIniciales)
}

// ReservarApuesta descuenta fichas al apostar en modo torneo (HU2-09). No
// mueve el saldo "a mano": guarda la reserva por apuesta y recalcula el total,
// para que un reintento con la misma apuesta no descuente dos veces (RNF-12).
//
// Devuelve el estado del jugador despues de la reserva. Rechaza si no hay
// fichas suficientes (quien llega a 0 no puede apostar) o si la apuesta ya
// existe con otro monto/opcion.
func (a *Almacen) ReservarApuesta(ctx context.Context, eventoID string, r Reserva) (Estado, error) {
	if eventoID == "" || r.ApuestaID == "" || r.UsuarioID == "" || r.Monto <= 0 || r.Multiplic <= 0 {
		return Estado{}, ErrPeticionInvalida
	}
	activo, err := a.store.TorneoActivo(ctx, eventoID)
	if err != nil {
		return Estado{}, err
	}
	if !activo {
		return Estado{}, ErrTorneoNoActivo
	}

	// ¿Ya existe esta apuesta? Reintento idempotente: valida el contenido y
	// devuelve el estado actual sin volver a descontar.
	previa, existe, err := a.store.Reserva(ctx, eventoID, r.ApuestaID)
	if err != nil {
		return Estado{}, err
	}
	if existe {
		if previa.Monto != r.Monto || previa.Opcion != r.Opcion {
			return Estado{}, ErrReservaIncompatible
		}
		return a.store.Recalcular(ctx, eventoID, r.UsuarioID)
	}

	// Comprobar saldo antes de reservar. El estado se recalcula desde Redis,
	// nunca se infiere en memoria, para resistir reinicios del Motor.
	estado, err := a.store.Recalcular(ctx, eventoID, r.UsuarioID)
	if err != nil {
		return Estado{}, err
	}
	if estado.Fichas <= 0 {
		return Estado{}, ErrFichasInsuficientes
	}
	if r.Monto > estado.Fichas {
		return Estado{}, ErrFichasInsuficientes
	}

	if r.ReservadaEn.IsZero() {
		r.ReservadaEn = a.ahora().UTC()
	}
	if _, err := a.store.GuardarReserva(ctx, eventoID, r); err != nil {
		return Estado{}, fmt.Errorf("guardar reserva de fichas: %w", err)
	}
	return a.store.Recalcular(ctx, eventoID, r.UsuarioID)
}

// Liquidar acredita el resultado de una apuesta de torneo (HU2-11, RF-15). Si
// acierta recibe monto x multiplicador en fichas; si falla no recibe nada (ya
// habia descontado el monto al reservar). Escribe el resultado una sola vez y
// recalcula el total: repetir la liquidacion no paga doble (RNF-12).
//
// Devuelve el estado del jugador despues de liquidar, para que el worker
// difunda las fichas ganadas y el nuevo puesto (RF-17).
func (a *Almacen) Liquidar(ctx context.Context, eventoID, apuestaID string, gano bool) (Estado, error) {
	if eventoID == "" || apuestaID == "" {
		return Estado{}, ErrPeticionInvalida
	}
	reserva, existe, err := a.store.Reserva(ctx, eventoID, apuestaID)
	if err != nil {
		return Estado{}, err
	}
	if !existe {
		// Sin reserva no se puede liquidar: la apuesta de torneo debio pasar
		// antes por ReservarApuesta (HU2-09).
		return Estado{}, ErrJugadorNoInscrito
	}

	pagadas := int64(0)
	if gano {
		// monto x multiplicador, truncado a entero: las fichas son enteras.
		pagadas = int64(float64(reserva.Monto) * reserva.Multiplic)
	}
	if _, err := a.store.GuardarResultado(ctx, eventoID, apuestaID, ResultadoApuesta{
		Gano:          gano,
		FichasPagadas: pagadas,
	}); err != nil {
		return Estado{}, fmt.Errorf("guardar resultado de fichas: %w", err)
	}
	return a.store.Recalcular(ctx, eventoID, reserva.UsuarioID)
}

// EstadoDe devuelve el estado actual de un jugador (saldo y apostado).
func (a *Almacen) EstadoDe(ctx context.Context, eventoID, usuarioID string) (Estado, error) {
	if eventoID == "" || usuarioID == "" {
		return Estado{}, ErrPeticionInvalida
	}
	return a.store.Recalcular(ctx, eventoID, usuarioID)
}

// Estados devuelve el estado de todos los inscritos (para armar el ranking).
func (a *Almacen) Estados(ctx context.Context, eventoID string) ([]Estado, error) {
	if eventoID == "" {
		return nil, ErrPeticionInvalida
	}
	return a.store.Estados(ctx, eventoID)
}

// Limpiar borra el estado del torneo en Redis. Lo llama HU2-12 cuando ya
// guardo el resultado final en event_entries (RF-16: "Redis se limpia").
func (a *Almacen) Limpiar(ctx context.Context, eventoID string) error {
	if eventoID == "" {
		return ErrPeticionInvalida
	}
	return a.store.Limpiar(ctx, eventoID)
}
