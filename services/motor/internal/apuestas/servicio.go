// Package apuestas implementa HU-06: apostar en un mercado abierto.
//
// Flujo (el front nunca toca el saldo; toda apuesta pasa por el motor):
//
//  1. El jugador envia POST /apuestas con mercado, opcion y monto.
//  2. El motor valida con el reloj del SERVIDOR que el mercado este abierto
//     (RNF-05): la cuenta regresiva del front es solo informativa.
//  3. El motor le pide a Pagos por REST que debite y registre la apuesta en
//     una sola transaccion (RNF-01), con clave de idempotencia.
//  4. Si Pagos acepta, el motor guarda el contexto de la apuesta en MongoDB.
//
// El motor nunca escribe en PostgreSQL. Este paquete solo depende de
// interfaces (Mercados, Pagos, Contextos) para poder probarse sin red ni BD.
package apuestas

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// Errores de negocio. El handler los traduce a codigos HTTP.
var (
	ErrPeticionInvalida  = errors.New("peticion invalida")
	ErrMercadoNoExiste   = errors.New("el mercado no existe")
	ErrMercadoCerrado    = errors.New("mercado cerrado")
	ErrOpcionInvalida    = errors.New("la opcion no pertenece al mercado")
	ErrSaldoInsuficiente = errors.New("saldo insuficiente")
	ErrUsuarioNoExiste   = errors.New("el usuario no existe")
	ErrPagosNoDisponible = errors.New("pagos no esta disponible")
	ErrPagosConflicto    = errors.New("pagos rechazo la idempotencia")
)

// Estado del mercado, como lo define el validador de la coleccion "mercados".
const EstadoAbierto = "abierto"

// Mercado es lo minimo que HU-06 necesita saber de un mercado. Lo crea y lo
// cierra HU-11; aqui solo se lee.
type Mercado struct {
	ID       string
	Estado   string
	Opciones []string
	CierraEn time.Time
}

// Peticion es lo que envia el jugador.
type Peticion struct {
	MercadoID string `json:"market_id"`
	Opcion    string `json:"option"`
	Monto     int64  `json:"amount"`
}

// Apuesta es la apuesta ya aceptada.
type Apuesta struct {
	ID                string  `json:"bet_id"`
	MercadoID         string  `json:"market_id"`
	Opcion            string  `json:"option"`
	Monto             int64   `json:"amount"`
	PuntosPotenciales float64 `json:"potential_points"`
}

// Debito es el cuerpo que se envia a Pagos (contrato por confirmar con
// Juan Diego). Los campos salen de la tabla bets y del ledger de Pagos.
type Debito struct {
	BetID          string `json:"bet_id"`
	UserID         string `json:"user_id"`
	MarketID       string `json:"market_id"`
	Option         string `json:"option"`
	Amount         int64  `json:"amount"`
	IdempotencyKey string `json:"idempotency_key"`
}

// Contexto es lo que el motor guarda en MongoDB (coleccion apuestas_contexto).
type Contexto struct {
	ApuestaID string
	MercadoID string
	UsuarioID string
	Opcion    string
	Monto     int64
	CreadaEn  time.Time
}

type Mercados interface {
	Obtener(ctx context.Context, id string) (Mercado, error) // ErrMercadoNoExiste si no esta
}

type Pagos interface {
	Debitar(ctx context.Context, d Debito) error
}

type Contextos interface {
	Guardar(ctx context.Context, c Contexto) error
}

// Servicio reune la logica de HU-06.
type Servicio struct {
	Mercados  Mercados
	Pagos     Pagos
	Contextos Contextos
	Ahora     func() time.Time // reloj del servidor; se puede reemplazar en pruebas
}

func NuevoServicio(m Mercados, p Pagos, c Contextos) *Servicio {
	return &Servicio{Mercados: m, Pagos: p, Contextos: c, Ahora: time.Now}
}

// PuntosPotenciales es la regla actual: acertar suma 10 + (monto x 0,5).
// Esta aislada en una sola funcion porque el grupo aun no cierra como
// funcionan los puntos; cambiarla no debe tocar nada mas.
func PuntosPotenciales(monto int64) float64 {
	return 10 + float64(monto)*0.5
}

// Apostar valida y registra una apuesta. claveCliente es opcional: si el
// cliente reenvia la misma clave (doble clic, reintento) se obtiene la misma
// apuesta y los tokens se debitan una sola vez (RNF-02).
func (s *Servicio) Apostar(ctx context.Context, userID string, p Peticion, claveCliente string) (Apuesta, error) {
	p.MercadoID = strings.TrimSpace(p.MercadoID)
	p.Opcion = strings.TrimSpace(p.Opcion)
	if userID == "" || p.MercadoID == "" || p.Opcion == "" || p.Monto <= 0 {
		return Apuesta{}, ErrPeticionInvalida
	}

	m, err := s.Mercados.Obtener(ctx, p.MercadoID)
	if err != nil {
		return Apuesta{}, err
	}
	// RNF-05: decide el reloj del servidor, no el del navegador.
	if m.Estado != EstadoAbierto || !s.Ahora().Before(m.CierraEn) {
		return Apuesta{}, ErrMercadoCerrado
	}
	if !contiene(m.Opciones, p.Opcion) {
		return Apuesta{}, ErrOpcionInvalida
	}

	betID := nuevoID()
	if claveCliente != "" {
		betID = idDeterminista(userID, claveCliente)
	}

	err = s.Pagos.Debitar(ctx, Debito{
		BetID:          betID,
		UserID:         userID,
		MarketID:       p.MercadoID,
		Option:         p.Opcion,
		Amount:         p.Monto,
		IdempotencyKey: "bet-debit:" + betID,
	})
	if err != nil {
		return Apuesta{}, err
	}

	err = s.Contextos.Guardar(ctx, Contexto{
		ApuestaID: betID,
		MercadoID: p.MercadoID,
		UsuarioID: userID,
		Opcion:    p.Opcion,
		Monto:     p.Monto,
		CreadaEn:  s.Ahora(),
	})
	if err != nil {
		// Pagos ya debito. Sin este registro el motor no podra liquidar la
		// apuesta. Prototipo: se avisa en el log; compensar (reversar el
		// debito) queda como decision pendiente del equipo.
		log.Printf("ATENCION: apuesta %s debitada pero sin contexto en MongoDB: %v", betID, err)
		return Apuesta{}, fmt.Errorf("apuesta debitada pero no registrada: %w", err)
	}

	return Apuesta{
		ID:                betID,
		MercadoID:         p.MercadoID,
		Opcion:            p.Opcion,
		Monto:             p.Monto,
		PuntosPotenciales: PuntosPotenciales(p.Monto),
	}, nil
}

func contiene(lista []string, v string) bool {
	for _, x := range lista {
		if x == v {
			return true
		}
	}
	return false
}

// nuevoID genera un UUID v4 sin dependencias externas.
func nuevoID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // sin entropia del sistema no se puede continuar
	}
	return formatoUUID(b)
}

// idDeterminista deriva un UUID estable de (usuario, clave): la misma clave
// del mismo usuario produce siempre el mismo id de apuesta.
func idDeterminista(userID, clave string) string {
	h := sha256.Sum256([]byte(userID + "|" + clave))
	var b [16]byte
	copy(b[:], h[:16])
	return formatoUUID(b)
}

func formatoUUID(b [16]byte) string {
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variante RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
