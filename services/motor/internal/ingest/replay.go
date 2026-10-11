package ingest

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Replay lee los eventos de una carrera desde Mongo y los emite por `out`
// respetando los intervalos originales, divididos por `factor`.
// factor=1 → tiempo real; factor=10 → 10x más rápido.
func (s *Servicio) Replay(ctx context.Context, carreraID string, factor float64, out chan<- EventoCarrera) error {
	if factor <= 0 {
		factor = 1
	}

	cur, err := s.eventos.Find(ctx, bson.M{"carrera_id": carreraID},
		options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}}))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	var previo time.Time
	for cur.Next(ctx) {
		var ev EventoCarrera
		if err := cur.Decode(&ev); err != nil {
			return err
		}

		if !previo.IsZero() {
			delta := ev.Timestamp.Sub(previo)
			espera := time.Duration(float64(delta) / factor)
			select {
			case <-time.After(espera):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		previo = ev.Timestamp

		select {
		case out <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return cur.Err()
}
