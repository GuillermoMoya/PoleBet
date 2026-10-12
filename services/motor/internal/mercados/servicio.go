// Package mercados implementa HU-11: el administrador abre un mercado y el
// reloj del servidor lo cierra.
//
// Ciclo de vida (lo decide SIEMPRE el reloj del servidor, RNF-05):
//
//		abierto ──(BETTING_WINDOW_SECONDS)──> cerrado ──(OBSERVATION_WINDOW_SECONDS)──> liquidar (HU-07)
//
//	 1. El admin envia POST /mercados con la pregunta y las opciones. El motor
//	    lo guarda en MongoDB con abre_en, cierra_en y observa_hasta, y lo
//	    difunde a todos por WebSocket (mercado_abierto).
//	 2. Al llegar cierra_en, el motor lo pasa a "cerrado" y lo difunde
//	    (mercado_cerrado). Desde ese instante HU-06 rechaza toda apuesta con
//	    «Mercado cerrado» (ademas HU-06 compara cierra_en con su propio reloj,
//	    asi que no depende de que este temporizador llegue a tiempo).
//	 3. Al llegar observa_hasta, el motor llama al Liquidador (HU-07).
//
// Si el motor se reinicia, Iniciar retoma los mercados que quedaron a medias.
// Solo puede haber un mercado abierto a la vez.
package mercados

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/arquisoft/motor/internal/ws"
)

// Estados del mercado, como los define el validador de la coleccion "mercados".
const (
	EstadoAbierto   = "abierto"
	EstadoCerrado   = "cerrado"
	EstadoLiquidado = "liquidado"
)

// Tipo de mercado del prototipo: un mercado Si/No sobre la carrera.
//
//	¿Habra algun cambio de posicion durante la ventana de observacion?
//
// Gana "Sí" si la reproduccion de la carrera emite al menos un evento de
// posicion entre cierra_en y observa_hasta; gana "No" si no emite ninguno
// mientras la reproduccion esta corriendo. La regla completa esta en
// internal/liquidacion y en documentation/hu-07-liquidacion-apuestas.md.
//
// Decisiones:
//   - El tipo lo fija Abrir; el admin no lo elige. El validador de la
//     coleccion "mercados" exige el campo tipo y el liquidador (HU-07) se
//     guia SOLO por el tipo, nunca por el texto de la pregunta.
//   - El contrato de POST /mercados no cambia ({pregunta, opciones}): el front
//     envia la pregunta y las opciones prellenadas en solo lectura, y aqui se
//     exige que las opciones sean exactamente [OpcionSi, OpcionNo]. Cuando haya
//     mas tipos, el admin elegira uno y cada tipo traera su pregunta fija.
//   - Estas constantes son la unica fuente de verdad en el motor: liquidacion
//     las importa de aqui. El front tiene una copia en src/mercados/mercado.ts.
const (
	TipoCambioPosicion     = "cambio_posicion"
	PreguntaCambioPosicion = "¿Habrá algún cambio de posición durante la ventana de observación?"
	OpcionSi               = "Sí"
	OpcionNo               = "No"
)

// Limites de la peticion. MaxLargoOpcion coincide con bets.option
// VARCHAR(100) de Pagos: una opcion mas larga no se podria apostar.
const (
	MaxLargoPregunta = 200
	MaxLargoOpcion   = 100
	MinOpciones      = 2
	MaxOpciones      = 10
)

// Errores de negocio. El handler los traduce a codigos HTTP.
var (
	ErrPeticionInvalida    = errors.New("peticion invalida")
	ErrCarreraNoExiste     = errors.New("la carrera no existe")
	ErrSinCarrera          = errors.New("no hay ninguna carrera cargada")
	ErrYaHayMercadoAbierto = errors.New("ya hay un mercado abierto")
	ErrNoHayMercado        = errors.New("no hay ningun mercado")
)

// ErrorValidacion explica que campo esta mal. errors.Is(err, ErrPeticionInvalida) es true.
type ErrorValidacion struct{ Mensaje string }

func (e ErrorValidacion) Error() string          { return e.Mensaje }
func (e ErrorValidacion) Is(objetivo error) bool { return objetivo == ErrPeticionInvalida }

// Peticion es lo que envia el administrador. carrera_id es opcional: si no
// viene se usa la ultima carrera cargada (HU-04).
type Peticion struct {
	CarreraID string   `json:"carrera_id"`
	Pregunta  string   `json:"pregunta"`
	Opciones  []string `json:"opciones"`
}

// Mercado es el mercado tal como se guarda.
type Mercado struct {
	ID           string
	CarreraID    string
	Tipo         string
	Pregunta     string
	Opciones     []string
	Estado       string
	AbreEn       time.Time
	CierraEn     time.Time
	ObservaHasta time.Time
	CreadoPor    string
}

// Vista es lo que ve el front, por REST y por WebSocket. Ahora es la hora del
// servidor al responder: el front la usa para que su cuenta regresiva no
// dependa del reloj del navegador (RNF-05).
type Vista struct {
	ID           string    `json:"id"`
	CarreraID    string    `json:"carrera_id"`
	Pregunta     string    `json:"pregunta"`
	Opciones     []string  `json:"opciones"`
	Estado       string    `json:"estado"`
	AbreEn       time.Time `json:"abre_en"`
	CierraEn     time.Time `json:"cierra_en"`
	ObservaHasta time.Time `json:"observa_hasta"`
	Ahora        time.Time `json:"ahora"`
}

// Repositorio guarda los mercados (MongoDB en produccion).
type Repositorio interface {
	CarreraMasReciente(ctx context.Context) (string, error) // ErrSinCarrera si no hay
	ExisteCarrera(ctx context.Context, carreraID string) (bool, error)
	Insertar(ctx context.Context, m Mercado) (string, error) // ErrYaHayMercadoAbierto si choca
	Actual(ctx context.Context) (Mercado, error)             // el mas reciente; ErrNoHayMercado
	Pendientes(ctx context.Context) ([]Mercado, error)       // abiertos o cerrados sin liquidar
	Cerrar(ctx context.Context, id string) (bool, error)     // abierto -> cerrado; false si ya no estaba abierto
}

// Difusor envia un mensaje a todos los conectados (el hub de WebSocket).
type Difusor interface {
	Difundir(m ws.Mensaje) error
}

// Liquidador lo implementa HU-07. Se llama una vez por mercado al terminar
// la ventana de observacion; tras un reinicio del motor puede volver a
// llamarse para el mismo mercado, asi que debe ser idempotente (RNF-02).
type Liquidador interface {
	Liquidar(ctx context.Context, m Mercado) error
}

// Servicio reune la logica de HU-11.
type Servicio struct {
	Repo               Repositorio
	Difusor            Difusor
	Liquidador         Liquidador // nil mientras HU-07 no exista
	VentanaApuestas    time.Duration
	VentanaObservacion time.Duration

	// Se pueden reemplazar en pruebas.
	Ahora   func() time.Time
	Despues func(d time.Duration, f func()) // programa f dentro de d

	mu   sync.Mutex      // serializa Abrir: un solo mercado abierto a la vez
	base context.Context // se cancela al apagar el motor
}

func NuevoServicio(repo Repositorio, difusor Difusor, ventanaApuestas, ventanaObservacion time.Duration) *Servicio {
	return &Servicio{
		Repo:               repo,
		Difusor:            difusor,
		VentanaApuestas:    ventanaApuestas,
		VentanaObservacion: ventanaObservacion,
		Ahora:              time.Now,
		Despues:            func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		base:               context.Background(),
	}
}

// Iniciar retoma los mercados que quedaron abiertos o en observacion (p. ej.
// tras reiniciar el motor): cierra los vencidos y programa los demas. Los
// temporizadores no hacen nada despues de cancelar ctx.
func (s *Servicio) Iniciar(ctx context.Context) error {
	s.base = ctx
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pendientes, err := s.Repo.Pendientes(ctx)
	if err != nil {
		return fmt.Errorf("no se pudieron leer los mercados pendientes: %w", err)
	}
	for _, m := range pendientes {
		switch {
		case m.Estado == EstadoAbierto && !s.Ahora().Before(m.CierraEn):
			s.cerrar(ctx, m) // vencio mientras el motor estaba apagado
		case m.Estado == EstadoAbierto:
			s.programarCierre(m)
		case m.Estado == EstadoCerrado:
			s.programarFinObservacion(m)
		}
	}
	if len(pendientes) > 0 {
		log.Printf("mercados: %d mercado(s) retomado(s) al iniciar", len(pendientes))
	}
	return nil
}

// Abrir valida la peticion, crea el mercado ya abierto y lo difunde.
func (s *Servicio) Abrir(ctx context.Context, adminID string, p Peticion) (Vista, error) {
	p, err := normalizar(p)
	if err != nil {
		return Vista{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.liberarMercadoVencido(ctx); err != nil {
		return Vista{}, err
	}

	if p.CarreraID == "" {
		if p.CarreraID, err = s.Repo.CarreraMasReciente(ctx); err != nil {
			return Vista{}, err
		}
	} else {
		existe, err := s.Repo.ExisteCarrera(ctx, p.CarreraID)
		if err != nil {
			return Vista{}, err
		}
		if !existe {
			return Vista{}, ErrCarreraNoExiste
		}
	}

	// MongoDB guarda milisegundos: se trunca para que lo que se difunde sea
	// igual a lo que queda guardado.
	abre := s.Ahora().UTC().Truncate(time.Millisecond)
	m := Mercado{
		CarreraID:    p.CarreraID,
		Tipo:         TipoCambioPosicion,
		Pregunta:     p.Pregunta,
		Opciones:     p.Opciones,
		Estado:       EstadoAbierto,
		AbreEn:       abre,
		CierraEn:     abre.Add(s.VentanaApuestas),
		ObservaHasta: abre.Add(s.VentanaApuestas + s.VentanaObservacion),
		CreadoPor:    adminID,
	}
	if m.ID, err = s.Repo.Insertar(ctx, m); err != nil {
		return Vista{}, err
	}

	log.Printf("mercados: abierto %s (%q) hasta %s", m.ID, m.Pregunta, m.CierraEn.Format(time.RFC3339))
	s.difundir(ws.TipoMercadoAbierto, m)
	s.programarCierre(m)
	return s.vista(m), nil
}

// Actual devuelve el mercado mas reciente, en el estado que este.
func (s *Servicio) Actual(ctx context.Context) (Vista, error) {
	m, err := s.Repo.Actual(ctx)
	if err != nil {
		return Vista{}, err
	}
	return s.vista(m), nil
}

// liberarMercadoVencido: si el mercado actual sigue "abierto" pero su
// ventana ya paso (el temporizador aun no corrio), lo cierra ya. Si sigue
// dentro de su ventana, no se puede abrir otro.
func (s *Servicio) liberarMercadoVencido(ctx context.Context) error {
	actual, err := s.Repo.Actual(ctx)
	if errors.Is(err, ErrNoHayMercado) {
		return nil
	}
	if err != nil {
		return err
	}
	if actual.Estado != EstadoAbierto {
		return nil
	}
	if s.Ahora().Before(actual.CierraEn) {
		return ErrYaHayMercadoAbierto
	}
	s.cerrar(ctx, actual)
	return nil
}

func (s *Servicio) programarCierre(m Mercado) {
	s.Despues(hasta(s.Ahora(), m.CierraEn), func() {
		if s.base.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(s.base, 10*time.Second)
		defer cancel()
		s.cerrar(ctx, m)
	})
}

// cerrar pasa el mercado a "cerrado" solo si seguia abierto, y solo en ese
// caso lo difunde y programa el fin de la observacion. Asi, si dos caminos
// intentan cerrarlo (temporizador y Abrir), solo uno lo hace.
func (s *Servicio) cerrar(ctx context.Context, m Mercado) {
	cambio, err := s.Repo.Cerrar(ctx, m.ID)
	if err != nil {
		log.Printf("mercados: no se pudo cerrar %s: %v", m.ID, err)
		return
	}
	if !cambio {
		return
	}
	m.Estado = EstadoCerrado
	log.Printf("mercados: cerrado %s, en observacion hasta %s", m.ID, m.ObservaHasta.Format(time.RFC3339))
	s.difundir(ws.TipoMercadoCerrado, m)
	s.programarFinObservacion(m)
}

func (s *Servicio) programarFinObservacion(m Mercado) {
	s.Despues(hasta(s.Ahora(), m.ObservaHasta), func() {
		if s.base.Err() != nil {
			return
		}
		if s.Liquidador == nil {
			log.Printf("mercados: termino la observacion de %s; lo liquida el worker de HU-07", m.ID)
			return
		}
		ctx, cancel := context.WithTimeout(s.base, time.Minute)
		defer cancel()
		if err := s.Liquidador.Liquidar(ctx, m); err != nil {
			log.Printf("mercados: fallo la liquidacion de %s: %v", m.ID, err)
		}
	})
}

func (s *Servicio) difundir(tipo string, m Mercado) {
	if err := s.Difusor.Difundir(ws.Mensaje{Type: tipo, Payload: s.vista(m)}); err != nil {
		log.Printf("mercados: no se pudo difundir %s de %s: %v", tipo, m.ID, err)
	}
}

func (s *Servicio) vista(m Mercado) Vista {
	return Vista{
		ID:           m.ID,
		CarreraID:    m.CarreraID,
		Pregunta:     m.Pregunta,
		Opciones:     m.Opciones,
		Estado:       m.Estado,
		AbreEn:       m.AbreEn,
		CierraEn:     m.CierraEn,
		ObservaHasta: m.ObservaHasta,
		Ahora:        s.Ahora().UTC(),
	}
}

// hasta devuelve cuanto falta para t (0 si ya paso).
func hasta(ahora, t time.Time) time.Duration {
	if d := t.Sub(ahora); d > 0 {
		return d
	}
	return 0
}

// normalizar limpia espacios y valida la peticion.
func normalizar(p Peticion) (Peticion, error) {
	p.CarreraID = strings.TrimSpace(p.CarreraID)
	p.Pregunta = strings.TrimSpace(p.Pregunta)

	if p.Pregunta == "" {
		return p, ErrorValidacion{"Escribe la pregunta del mercado"}
	}
	if utf8.RuneCountInString(p.Pregunta) > MaxLargoPregunta {
		return p, ErrorValidacion{fmt.Sprintf("La pregunta no puede tener mas de %d caracteres", MaxLargoPregunta)}
	}
	if len(p.Opciones) < MinOpciones || len(p.Opciones) > MaxOpciones {
		return p, ErrorValidacion{fmt.Sprintf("El mercado debe tener entre %d y %d opciones", MinOpciones, MaxOpciones)}
	}

	opciones := make([]string, 0, len(p.Opciones))
	vistas := make(map[string]bool, len(p.Opciones))
	for _, o := range p.Opciones {
		o = strings.TrimSpace(o)
		if o == "" {
			return p, ErrorValidacion{"Ninguna opcion puede estar vacia"}
		}
		if utf8.RuneCountInString(o) > MaxLargoOpcion {
			return p, ErrorValidacion{fmt.Sprintf("Cada opcion puede tener maximo %d caracteres", MaxLargoOpcion)}
		}
		clave := strings.ToLower(o)
		if vistas[clave] {
			return p, ErrorValidacion{fmt.Sprintf("La opcion %q esta repetida", o)}
		}
		vistas[clave] = true
		opciones = append(opciones, o)
	}
	// El unico tipo de mercado (TipoCambioPosicion) se liquida a "Sí" o "No":
	// cualquier otra opcion nunca podria ganar. Se exige el orden exacto para
	// que el front y el motor muestren siempre lo mismo.
	if len(opciones) != 2 || opciones[0] != OpcionSi || opciones[1] != OpcionNo {
		return p, ErrorValidacion{fmt.Sprintf("Este mercado solo admite las opciones %q y %q, en ese orden", OpcionSi, OpcionNo)}
	}
	p.Opciones = opciones
	return p, nil
}
