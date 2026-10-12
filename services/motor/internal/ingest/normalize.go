package ingest

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ---- Tipos que devuelve OpenF1 ----

type PosicionOpenF1 struct {
	Date         time.Time `json:"date"`
	DriverNumber int       `json:"driver_number"`
	Position     int       `json:"position"`
	SessionKey   int       `json:"session_key"`
}

type PilotoOpenF1 struct {
	DriverNumber int    `json:"driver_number"`
	FullName     string `json:"full_name"`
	NameAcronym  string `json:"name_acronym"`
	TeamName     string `json:"team_name"`
	SessionKey   int    `json:"session_key"`
}

type SesionOpenF1 struct {
	SessionKey  int       `json:"session_key"`
	SessionName string    `json:"session_name"`
	MeetingKey  int       `json:"meeting_key"`
	Location    string    `json:"location"`
	CountryName string    `json:"country_name"`
	DateStart   time.Time `json:"date_start"`
	DateEnd     time.Time `json:"date_end"`
	Year        int       `json:"year"`
}

// ---- Tipo normalizado que va a MongoDB ----

// EventoCarrera es un documento de la coleccion eventos_carrera. Tiene tres
// fechas con significados distintos:
//
//   - Timestamp: momento del evento EN LA CARRERA (el `date` de OpenF1). Para
//     una carrera pasada es historico (p. ej. 2024): sirve para ordenar y
//     espaciar la reproduccion, no para compararlo con el reloj del servidor.
//   - LeidoEn: hora real en que HU-04 lo descargo de OpenF1 (auditoria).
//   - EmitidoEn: hora real en que la reproduccion (Replay) lo emitio por
//     WebSocket. Es la que usa la liquidacion (HU-07) para saber si "paso
//     algo" durante la ventana de observacion de un mercado. Vacia hasta que
//     el evento se reproduce; se sobrescribe si se vuelve a reproducir.
type EventoCarrera struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"        json:"-"`
	CarreraID string             `bson:"carrera_id"           json:"carrera_id"`
	Timestamp time.Time          `bson:"timestamp"            json:"timestamp"`
	Tipo      string             `bson:"tipo"                 json:"tipo"`
	Datos     map[string]any     `bson:"datos"                json:"datos"`
	LeidoEn   time.Time          `bson:"leido_en"             json:"leido_en"`
	EmitidoEn time.Time          `bson:"emitido_en,omitempty" json:"emitido_en,omitzero"`
}

func NormalizarPosicion(carreraID string, p PosicionOpenF1) EventoCarrera {
	return EventoCarrera{
		CarreraID: carreraID,
		Timestamp: p.Date.UTC(),
		Tipo:      "posicion",
		Datos: map[string]any{
			"driver_number": p.DriverNumber,
			"position":      p.Position,
			"session_key":   p.SessionKey,
		},
		LeidoEn: time.Now().UTC(),
	}
}

// NormalizarPilotos genera un único evento "vuelta" con la lista de pilotos
// como contexto. Se guarda en eventos_carrera para que el front lo pueda
// resolver sin volver a llamar a OpenF1.
func NormalizarPilotos(carreraID string, pilotos []PilotoOpenF1) EventoCarrera {
	items := make([]map[string]any, 0, len(pilotos))
	for _, p := range pilotos {
		items = append(items, map[string]any{
			"driver_number": p.DriverNumber,
			"full_name":     p.FullName,
			"name_acronym":  p.NameAcronym,
			"team_name":     p.TeamName,
		})
	}
	return EventoCarrera{
		CarreraID: carreraID,
		Timestamp: time.Now().UTC(),
		Tipo:      "vuelta",
		Datos: map[string]any{
			"tipo_vuelta": "lista_pilotos",
			"pilotos":     items,
		},
		LeidoEn: time.Now().UTC(),
	}
}
