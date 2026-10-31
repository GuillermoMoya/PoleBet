package fichas

import (
	"context"
	"log"
	"time"

	"github.com/arquisoft/motor/internal/ws"
)

// Notificador es lo que el Difusor usa del hub de WebSocket. *ws.Hub lo
// cumple; en las pruebas se reemplaza por un hub falso.
type Notificador interface {
	Difundir(ws.Mensaje) error
}

// ParametrosTorneo son los datos del evento que el ranking necesita y que no
// viven en Redis sino en Pagos (events): fichas iniciales, el minimo a apostar
// y el botin. Los entrega el flujo de largada (HU2-06) y se conservan mientras
// el torneo este activo.
type ParametrosTorneo struct {
	FichasIniciales int64
	MinWagerPct     float64
	Botin           int64
}

// Difusor arma el ranking del torneo y lo difunde por WebSocket (RF-18). Sigue
// el patron de posiciones.Difusor (HU-05): cada cambio recalcula la foto
// completa y la envia a todos, marcando emitido_en para medir la latencia
// (RNF-13, cambio en pantalla en < 1 s).
type Difusor struct {
	almacen *Almacen
	hub     Notificador
	params  ParametrosTorneo
	ahora   func() time.Time
}

func NuevoDifusor(almacen *Almacen, hub Notificador, params ParametrosTorneo) *Difusor {
	return &Difusor{almacen: almacen, hub: hub, params: params, ahora: time.Now}
}

// Difundir lee el estado actual de todos los inscritos, arma el ranking y lo
// envia. Se llama despues de cada cambio de fichas (una liquidacion de
// torneo). Si falla solo se registra: el cambio ya quedo en Redis.
func (d *Difusor) Difundir(ctx context.Context, eventoID string) {
	estados, err := d.almacen.Estados(ctx, eventoID)
	if err != nil {
		log.Printf("HU2-11: no se pudo leer el estado del torneo %s: %v", eventoID, err)
		return
	}
	ranking := ConstruirRanking(
		eventoID, estados,
		d.params.FichasIniciales, d.params.MinWagerPct, d.params.Botin,
		d.ahora(),
	)
	ranking.EmitidoEn = d.ahora().UTC()
	if err := d.hub.Difundir(ws.Mensaje{Type: ws.TipoRankingTorneo, Payload: ranking}); err != nil {
		log.Printf("HU2-11: no se pudo difundir el ranking del torneo %s: %v", eventoID, err)
	}
}
