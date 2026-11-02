package ws

// Mensaje es el sobre comun de todo lo que el motor envia por WebSocket.
// El front decide que hacer segun Type; la forma de Payload depende del tipo
// (ver README del motor).
type Mensaje struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Tipos de mensaje acordados. Agregar aqui cualquier tipo nuevo para que el
// front y los demas modulos usen el mismo nombre.
const (
	TipoPosiciones       = "posiciones"        // difusion (HU-05)
	TipoMercadoAbierto   = "mercado_abierto"   // difusion (HU-11)
	TipoMercadoCerrado   = "mercado_cerrado"   // difusion (HU-11)
	TipoMercadoLiquidado = "mercado_liquidado" // difusion (HU-07)
	TipoResultadoApuesta = "resultado_apuesta" // a un usuario (HU-08)
	TipoRankingTorneo    = "ranking_torneo"    // difusion (HU2-11)
)
