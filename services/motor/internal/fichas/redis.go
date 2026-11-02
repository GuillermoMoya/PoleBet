package fichas

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore implementa Store sobre Redis. Es el dueño de las fichas y el
// ranking del torneo en curso (comentario del docker-compose: "motor = dueño
// de fichas y ranking"). Ningun otro servicio escribe estas claves.
//
// Esquema de claves (prefijo por evento para poder limpiarlo de una vez):
//
//	torneo:{evento}:inicial                -> String: fichas iniciales (marca "torneo activo")
//	torneo:{evento}:inscritos              -> Set: usuario_id de cada inscrito
//	torneo:{evento}:apuestas:{usuario}     -> Set: apuesta_id de ese usuario
//	torneo:{evento}:reserva:{apuesta}      -> Hash: usuario, monto, opcion, multiplicador
//	torneo:{evento}:resultado:{apuesta}    -> Hash: gano, pagadas
//	torneo:{evento}:fichas                 -> Hash: usuario -> saldo recalculado (cache del ranking)
//	torneo:{evento}:apostado               -> Hash: usuario -> apostado recalculado
//
// Las dos ultimas son cache derivada: se reescriben en cada Recalcular a
// partir de reservas y resultados, nunca con INCRBY. Asi el total jamas se
// "suma" dos veces (RNF-12) y sobrevive a reinicios del Motor.
type RedisStore struct {
	rdb *redis.Client
	// ttl limita cuanto viven las claves de un evento, por si HU2-12 no llega
	// a limpiarlas (torneo abandonado). 0 = sin expiracion.
	ttl time.Duration
}

func NuevoRedisStore(rdb *redis.Client, ttl time.Duration) *RedisStore {
	return &RedisStore{rdb: rdb, ttl: ttl}
}

// Verifica en compilacion que RedisStore cumple el contrato Store.
var _ Store = (*RedisStore)(nil)

func (s *RedisStore) kInicial(e string) string   { return "torneo:" + e + ":inicial" }
func (s *RedisStore) kInscritos(e string) string { return "torneo:" + e + ":inscritos" }
func (s *RedisStore) kApuestas(e, u string) string {
	return "torneo:" + e + ":apuestas:" + u
}
func (s *RedisStore) kReserva(e, a string) string   { return "torneo:" + e + ":reserva:" + a }
func (s *RedisStore) kResultado(e, a string) string { return "torneo:" + e + ":resultado:" + a }
func (s *RedisStore) kFichas(e string) string       { return "torneo:" + e + ":fichas" }
func (s *RedisStore) kApostado(e string) string     { return "torneo:" + e + ":apostado" }

func (s *RedisStore) InicializarJugadores(ctx context.Context, eventoID string, jugadores []string, fichasIniciales int64) error {
	// SetNX en la clave "inicial" asegura que solo la primera largada fija las
	// fichas iniciales; repetirla no reinicia el torneo (idempotente).
	ok, err := s.rdb.SetNX(ctx, s.kInicial(eventoID), fichasIniciales, s.ttl).Result()
	if err != nil {
		return fmt.Errorf("fijar fichas iniciales: %w", err)
	}
	if !ok {
		// El torneo ya estaba inicializado: no se vuelve a crear a nadie.
		return nil
	}

	pipe := s.rdb.TxPipeline()
	args := make([]any, 0, len(jugadores))
	fichas := make([]any, 0, len(jugadores)*2)
	apostado := make([]any, 0, len(jugadores)*2)
	for _, u := range jugadores {
		args = append(args, u)
		fichas = append(fichas, u, fichasIniciales)
		apostado = append(apostado, u, 0)
	}
	pipe.SAdd(ctx, s.kInscritos(eventoID), args...)
	pipe.HSet(ctx, s.kFichas(eventoID), fichas...)
	pipe.HSet(ctx, s.kApostado(eventoID), apostado...)
	if s.ttl > 0 {
		pipe.Expire(ctx, s.kInscritos(eventoID), s.ttl)
		pipe.Expire(ctx, s.kFichas(eventoID), s.ttl)
		pipe.Expire(ctx, s.kApostado(eventoID), s.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("crear fichas de los inscritos: %w", err)
	}
	return nil
}

func (s *RedisStore) TorneoActivo(ctx context.Context, eventoID string) (bool, error) {
	n, err := s.rdb.Exists(ctx, s.kInicial(eventoID)).Result()
	if err != nil {
		return false, fmt.Errorf("consultar torneo activo: %w", err)
	}
	return n > 0, nil
}

func (s *RedisStore) fichasIniciales(ctx context.Context, eventoID string) (int64, error) {
	v, err := s.rdb.Get(ctx, s.kInicial(eventoID)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, ErrTorneoNoActivo
	}
	if err != nil {
		return 0, fmt.Errorf("leer fichas iniciales: %w", err)
	}
	return v, nil
}

func (s *RedisStore) GuardarReserva(ctx context.Context, eventoID string, r Reserva) (bool, error) {
	// HSetNX sobre un campo centinela para decidir si la reserva es nueva sin
	// una condicion de carrera: solo el primero en crear "usuario" gana.
	clave := s.kReserva(eventoID, r.ApuestaID)
	nueva, err := s.rdb.HSetNX(ctx, clave, "usuario", r.UsuarioID).Result()
	if err != nil {
		return false, fmt.Errorf("guardar reserva: %w", err)
	}
	if !nueva {
		return false, nil
	}
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, clave,
		"monto", r.Monto,
		"opcion", r.Opcion,
		"multiplicador", strconv.FormatFloat(r.Multiplic, 'f', -1, 64),
		"reservada_en", r.ReservadaEn.UTC().Format(time.RFC3339Nano),
	)
	pipe.SAdd(ctx, s.kApuestas(eventoID, r.UsuarioID), r.ApuestaID)
	if s.ttl > 0 {
		pipe.Expire(ctx, clave, s.ttl)
		pipe.Expire(ctx, s.kApuestas(eventoID, r.UsuarioID), s.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("completar reserva: %w", err)
	}
	return true, nil
}

func (s *RedisStore) Reserva(ctx context.Context, eventoID, apuestaID string) (Reserva, bool, error) {
	m, err := s.rdb.HGetAll(ctx, s.kReserva(eventoID, apuestaID)).Result()
	if err != nil {
		return Reserva{}, false, fmt.Errorf("leer reserva: %w", err)
	}
	if len(m) == 0 {
		return Reserva{}, false, nil
	}
	monto, _ := strconv.ParseInt(m["monto"], 10, 64)
	mult, _ := strconv.ParseFloat(m["multiplicador"], 64)
	reservada, _ := time.Parse(time.RFC3339Nano, m["reservada_en"])
	return Reserva{
		ApuestaID:   apuestaID,
		UsuarioID:   m["usuario"],
		Monto:       monto,
		Opcion:      m["opcion"],
		Multiplic:   mult,
		ReservadaEn: reservada,
	}, true, nil
}

func (s *RedisStore) GuardarResultado(ctx context.Context, eventoID, apuestaID string, res ResultadoApuesta) (bool, error) {
	clave := s.kResultado(eventoID, apuestaID)
	gano := "0"
	if res.Gano {
		gano = "1"
	}
	// HSetNX sobre "gano": si ya existe, el resultado ya se escribio y no se
	// vuelve a aplicar (idempotencia de la liquidacion).
	nueva, err := s.rdb.HSetNX(ctx, clave, "gano", gano).Result()
	if err != nil {
		return false, fmt.Errorf("guardar resultado: %w", err)
	}
	if !nueva {
		return false, nil
	}
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, clave, "pagadas", res.FichasPagadas)
	if s.ttl > 0 {
		pipe.Expire(ctx, clave, s.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("completar resultado: %w", err)
	}
	return true, nil
}

// Recalcular reconstruye el saldo del jugador: fichas iniciales - lo apostado
// + lo pagado por las apuestas ya resueltas. Lee reservas y resultados de
// Redis y reescribe las claves cache (fichas/apostado). Nunca usa INCRBY, por
// eso repetir liquidaciones o reservas no descuadra las fichas (RNF-12).
func (s *RedisStore) Recalcular(ctx context.Context, eventoID, usuarioID string) (Estado, error) {
	inicial, err := s.fichasIniciales(ctx, eventoID)
	if err != nil {
		return Estado{}, err
	}
	apuestas, err := s.rdb.SMembers(ctx, s.kApuestas(eventoID, usuarioID)).Result()
	if err != nil {
		return Estado{}, fmt.Errorf("leer apuestas del jugador: %w", err)
	}

	saldo := inicial
	var apostado int64
	for _, apuestaID := range apuestas {
		reserva, existe, err := s.Reserva(ctx, eventoID, apuestaID)
		if err != nil {
			return Estado{}, err
		}
		if !existe {
			continue
		}
		apostado += reserva.Monto
		saldo -= reserva.Monto // al reservar se descuenta el monto apostado

		// Si la apuesta ya se resolvio, sumar lo pagado (0 si perdio).
		pagadas, resuelta, err := s.resultadoPagadas(ctx, eventoID, apuestaID)
		if err != nil {
			return Estado{}, err
		}
		if resuelta {
			saldo += pagadas
		}
	}
	if saldo < 0 {
		saldo = 0 // nunca se muestra saldo negativo (regla del torneo)
	}

	// Reescribir la cache del ranking (no acumulativa).
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, s.kFichas(eventoID), usuarioID, saldo)
	pipe.HSet(ctx, s.kApostado(eventoID), usuarioID, apostado)
	if _, err := pipe.Exec(ctx); err != nil {
		return Estado{}, fmt.Errorf("actualizar cache de fichas: %w", err)
	}
	return Estado{UsuarioID: usuarioID, Fichas: saldo, Apostado: apostado}, nil
}

func (s *RedisStore) resultadoPagadas(ctx context.Context, eventoID, apuestaID string) (int64, bool, error) {
	m, err := s.rdb.HGetAll(ctx, s.kResultado(eventoID, apuestaID)).Result()
	if err != nil {
		return 0, false, fmt.Errorf("leer resultado: %w", err)
	}
	if len(m) == 0 {
		return 0, false, nil
	}
	pagadas, _ := strconv.ParseInt(m["pagadas"], 10, 64)
	return pagadas, true, nil
}

func (s *RedisStore) Estados(ctx context.Context, eventoID string) ([]Estado, error) {
	inscritos, err := s.rdb.SMembers(ctx, s.kInscritos(eventoID)).Result()
	if err != nil {
		return nil, fmt.Errorf("leer inscritos: %w", err)
	}
	fichas, err := s.rdb.HGetAll(ctx, s.kFichas(eventoID)).Result()
	if err != nil {
		return nil, fmt.Errorf("leer fichas: %w", err)
	}
	apostado, err := s.rdb.HGetAll(ctx, s.kApostado(eventoID)).Result()
	if err != nil {
		return nil, fmt.Errorf("leer apostado: %w", err)
	}
	estados := make([]Estado, 0, len(inscritos))
	for _, u := range inscritos {
		f, _ := strconv.ParseInt(fichas[u], 10, 64)
		a, _ := strconv.ParseInt(apostado[u], 10, 64)
		estados = append(estados, Estado{UsuarioID: u, Fichas: f, Apostado: a})
	}
	return estados, nil
}

func (s *RedisStore) Limpiar(ctx context.Context, eventoID string) error {
	// Borrar por patron con SCAN para no bloquear Redis con KEYS en produccion.
	patron := "torneo:" + eventoID + ":*"
	var cursor uint64
	for {
		claves, nuevoCursor, err := s.rdb.Scan(ctx, cursor, patron, 200).Result()
		if err != nil {
			return fmt.Errorf("escanear claves del torneo: %w", err)
		}
		if len(claves) > 0 {
			if err := s.rdb.Del(ctx, claves...).Err(); err != nil {
				return fmt.Errorf("borrar claves del torneo: %w", err)
			}
		}
		cursor = nuevoCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}
