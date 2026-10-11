package ingest

import (
	"context"
	"fmt"
	"log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Servicio combina OpenF1 + MongoDB.
type Servicio struct {
	openf1  *OpenF1Client
	eventos *mongo.Collection
}

func NuevoServicio(db *mongo.Database) *Servicio {
	return &Servicio{
		openf1:  NuevoOpenF1Client(),
		eventos: db.Collection("eventos_carrera"),
	}
}

// IngestaOpenF1 trae posiciones y pilotos de una sesión de OpenF1, los
// normaliza y los guarda en eventos_carrera.
func (s *Servicio) IngestaOpenF1(ctx context.Context, carreraID string, sessionKey int) (int, error) {
	posiciones, err := s.openf1.Posiciones(ctx, sessionKey)
	if err != nil {
		return 0, fmt.Errorf("leyendo posiciones: %w", err)
	}
	pilotos, err := s.openf1.Pilotos(ctx, sessionKey)
	if err != nil {
		return 0, fmt.Errorf("leyendo pilotos: %w", err)
	}

	if len(posiciones) == 0 && len(pilotos) == 0 {
		return 0, fmt.Errorf("OpenF1 no devolvio datos para session_key=%d", sessionKey)
	}

	// Idempotencia: borrar eventos previos de esta carrera antes de insertar.
	// Asi, ejecutar dos veces el endpoint no duplica datos.
	if _, err := s.eventos.DeleteMany(ctx, bson.M{"carrera_id": carreraID}); err != nil {
		return 0, fmt.Errorf("borrando eventos previos: %w", err)
	}

	docs := make([]any, 0, len(posiciones)+1)
	for _, p := range posiciones {
		docs = append(docs, NormalizarPosicion(carreraID, p))
	}
	if len(pilotos) > 0 {
		docs = append(docs, NormalizarPilotos(carreraID, pilotos))
	}

	res, err := s.eventos.InsertMany(ctx, docs)
	if err != nil {
		return 0, fmt.Errorf("insertando eventos: %w", err)
	}

	log.Printf("ingesta: %d eventos guardados para %s (session_key=%d)",
		len(res.InsertedIDs), carreraID, sessionKey)
	return len(res.InsertedIDs), nil
}
