package liquidacion

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mongoStore struct {
	mercados  *mongo.Collection
	eventos   *mongo.Collection
	contextos *mongo.Collection
}

func NuevoMongoStore(db *mongo.Database) Store {
	return &mongoStore{
		mercados:  db.Collection("mercados"),
		eventos:   db.Collection("eventos_carrera"),
		contextos: db.Collection("apuestas_contexto"),
	}
}

type mercadoDoc struct {
	ID           primitive.ObjectID `bson:"_id"`
	CarreraID    string             `bson:"carrera_id"`
	Tipo         string             `bson:"tipo"`
	Estado       string             `bson:"estado"`
	Opciones     []string           `bson:"opciones"`
	CierraEn     time.Time          `bson:"cierra_en"`
	ObservaHasta time.Time          `bson:"observa_hasta"`
}

func (d mercadoDoc) modelo() Mercado {
	return Mercado{
		ID: d.ID.Hex(), CarreraID: d.CarreraID, Tipo: d.Tipo,
		Estado: d.Estado, Opciones: d.Opciones, CierraEn: d.CierraEn,
		ObservaHasta: d.ObservaHasta,
	}
}

func (s *mongoStore) CerrarVencidos(ctx context.Context, ahora time.Time, observacion time.Duration) ([]Mercado, error) {
	cursor, err := s.mercados.Find(ctx, bson.M{"estado": "abierto", "cierra_en": bson.M{"$lte": ahora}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var cerrados []Mercado
	for cursor.Next(ctx) {
		var doc mercadoDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		observaHasta := doc.CierraEn.Add(observacion)
		resultado, err := s.mercados.UpdateOne(ctx,
			bson.M{"_id": doc.ID, "estado": "abierto", "cierra_en": bson.M{"$lte": ahora}},
			bson.M{"$set": bson.M{"estado": "cerrado", "observa_hasta": observaHasta}},
		)
		if err != nil {
			return nil, err
		}
		if resultado.ModifiedCount == 1 {
			doc.Estado = "cerrado"
			doc.ObservaHasta = observaHasta
			cerrados = append(cerrados, doc.modelo())
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return cerrados, nil
}

func (s *mongoStore) MercadosVencidos(ctx context.Context, ahora time.Time) ([]Mercado, error) {
	filtro := bson.M{
		"estado": "cerrado",
		"observa_hasta": bson.M{"$lte": ahora},
		"$or": bson.A{
			bson.M{"liquidacion_reintento_en": bson.M{"$exists": false}},
			bson.M{"liquidacion_reintento_en": bson.M{"$lte": ahora}},
		},
	}
	cursor, err := s.mercados.Find(ctx, filtro)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var mercados []Mercado
	for cursor.Next(ctx) {
		var doc mercadoDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		mercados = append(mercados, doc.modelo())
	}
	return mercados, cursor.Err()
}

func (s *mongoStore) Reclamar(ctx context.Context, id string, ahora time.Time, duracion time.Duration) (string, bool, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return "", false, fmt.Errorf("id de mercado inválido")
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", false, fmt.Errorf("generar lease: %w", err)
	}
	token := hex.EncodeToString(bytes[:])
	filtro := bson.M{
		"_id": oid, "estado": "cerrado", "observa_hasta": bson.M{"$lte": ahora},
		"$and": bson.A{
			bson.M{"$or": bson.A{
				bson.M{"liquidacion_lease_hasta": bson.M{"$exists": false}},
				bson.M{"liquidacion_lease_hasta": bson.M{"$lte": ahora}},
			}},
			bson.M{"$or": bson.A{
				bson.M{"liquidacion_reintento_en": bson.M{"$exists": false}},
				bson.M{"liquidacion_reintento_en": bson.M{"$lte": ahora}},
			}},
		},
	}
	resultado, err := s.mercados.UpdateOne(ctx, filtro, bson.M{"$set": bson.M{
		"liquidacion_lease":      token,
		"liquidacion_lease_hasta": ahora.Add(duracion),
	}})
	if err != nil {
		return "", false, err
	}
	return token, resultado.ModifiedCount == 1, nil
}

func (s *mongoStore) Datos(ctx context.Context, mercado Mercado) (DatosResolucion, error) {
	datos := DatosResolucion{}
	filtro := bson.M{
		"carrera_id": mercado.CarreraID,
		"tipo":       "posicion",
		"timestamp":  bson.M{"$gte": mercado.CierraEn, "$lte": mercado.ObservaHasta},
		"leido_en":   bson.M{"$gte": mercado.CierraEn, "$lte": mercado.ObservaHasta},
	}
	cursor, err := s.eventos.Find(ctx, filtro)
	if err != nil {
		return datos, err
	}
	for cursor.Next(ctx) {
		var evento struct {
			Timestamp time.Time `bson:"timestamp"`
			LeidoEn   time.Time `bson:"leido_en"`
			Datos     bson.M    `bson:"datos"`
		}
		if err := cursor.Decode(&evento); err != nil {
			_ = cursor.Close(ctx)
			return datos, err
		}
		datos.Muestras = append(datos.Muestras, MuestraPosicion{
			Timestamp:    evento.Timestamp,
			LeidoEn:      evento.LeidoEn,
			DriverNumber: entero(evento.Datos["driver_number"]),
			Position:     entero(evento.Datos["position"]),
			DriverName:   texto(evento.Datos["driver_name"]),
		})
	}
	if err := cursor.Err(); err != nil {
		_ = cursor.Close(ctx)
		return datos, err
	}
	if err := cursor.Close(ctx); err != nil {
		return datos, err
	}

	pilotoCursor, err := s.eventos.Find(ctx, bson.M{
		"carrera_id": mercado.CarreraID,
		"tipo":              "vuelta",
		"datos.tipo_vuelta": "lista_pilotos",
	}, options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}))
	if err != nil {
		return datos, err
	}
	defer pilotoCursor.Close(ctx)
	if pilotoCursor.Next(ctx) {
		var evento struct {
			Datos struct {
				Pilotos []struct {
					DriverNumber int    `bson:"driver_number"`
					FullName     string `bson:"full_name"`
					NameAcronym  string `bson:"name_acronym"`
				} `bson:"pilotos"`
			} `bson:"datos"`
		}
		if err := pilotoCursor.Decode(&evento); err != nil {
			return datos, err
		}
		for _, piloto := range evento.Datos.Pilotos {
			datos.Pilotos = append(datos.Pilotos, Piloto{
				DriverNumber: piloto.DriverNumber,
				FullName: piloto.FullName,
				NameAcronym: piloto.NameAcronym,
			})
		}
	}
	return datos, pilotoCursor.Err()
}

func (s *mongoStore) Contextos(ctx context.Context, mercadoID string) ([]ContextoApuesta, error) {
	oid, err := primitive.ObjectIDFromHex(mercadoID)
	if err != nil {
		return nil, errors.New("id de mercado inválido")
	}
	cursor, err := s.contextos.Find(ctx, bson.M{"mercado_id": oid})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var resultados []ContextoApuesta
	for cursor.Next(ctx) {
		var contexto struct {
			ApuestaID string `bson:"apuesta_id"`
			UsuarioID string `bson:"usuario_id"`
			Opcion string `bson:"opcion"`
			Monto int64 `bson:"monto"`
			Liquidada bool `bson:"liquidada"`
			Gano *bool `bson:"gano"`
			Puntos float64 `bson:"puntos_obtenidos"`
		}
		if err := cursor.Decode(&contexto); err != nil {
			return nil, err
		}
		resultados = append(resultados, ContextoApuesta{
			ApuestaID: contexto.ApuestaID, UsuarioID: contexto.UsuarioID,
			Opcion: contexto.Opcion, Monto: contexto.Monto,
			Liquidada: contexto.Liquidada, Gano: contexto.Gano, Puntos: contexto.Puntos,
		})
	}
	return resultados, cursor.Err()
}

func (s *mongoStore) GuardarResultadoApuesta(ctx context.Context, mercadoID string, contexto ContextoApuesta, gano bool, puntos float64) error {
	oid, err := primitive.ObjectIDFromHex(mercadoID)
	if err != nil {
		return errors.New("id de mercado inválido")
	}
	filtro := bson.M{
		"apuesta_id": contexto.ApuestaID,
		"mercado_id": oid,
		"$or": bson.A{
			bson.M{"liquidada": bson.M{"$ne": true}},
			bson.M{"liquidada": bson.M{"$exists": false}},
		},
	}
	resultado, err := s.contextos.UpdateOne(ctx, filtro, bson.M{"$set": bson.M{
		"liquidada": true,
		"gano": gano,
		"puntos_obtenidos": puntos,
	}})
	if err != nil {
		return err
	}
	if resultado.ModifiedCount == 1 {
		return nil
	}
	var previo struct {
		Liquidada bool `bson:"liquidada"`
		Gano *bool `bson:"gano"`
		Puntos float64 `bson:"puntos_obtenidos"`
	}
	if err := s.contextos.FindOne(ctx, bson.M{"apuesta_id": contexto.ApuestaID, "mercado_id": oid}).Decode(&previo); err != nil {
		return err
	}
	if !previo.Liquidada || previo.Gano == nil || *previo.Gano != gano || previo.Puntos != puntos {
		return errors.New("resultado previo del contexto no coincide")
	}
	return nil
}

func (s *mongoStore) Finalizar(ctx context.Context, mercado Mercado, token string, resultado LiquidacionMercado, ahora time.Time) error {
	oid, err := primitive.ObjectIDFromHex(mercado.ID)
	if err != nil {
		return errors.New("id de mercado inválido")
	}
	res, err := s.mercados.UpdateOne(ctx, bson.M{
		"_id": oid, "estado": "cerrado", "liquidacion_lease": token,
	}, bson.M{
		"$set": bson.M{
			"estado": "liquidado",
			"resultado": resultado.Resultado,
			"puntos_otorgados": resultado.PuntosOtorgados,
			"liquidado_en": ahora,
		},
		"$unset": bson.M{
			"liquidacion_error": "",
			"liquidacion_reintento_en": "",
			"liquidacion_lease": "",
			"liquidacion_lease_hasta": "",
		},
	})
	if err != nil {
		return err
	}
	if res.ModifiedCount != 1 {
		return errors.New("lease perdido antes de finalizar el mercado")
	}
	return nil
}

func (s *mongoStore) Reintentar(ctx context.Context, id, token, diagnostico string, siguiente time.Time) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return errors.New("id de mercado inválido")
	}
	res, err := s.mercados.UpdateOne(ctx, bson.M{
		"_id": oid, "estado": "cerrado", "liquidacion_lease": token,
	}, bson.M{
		"$set": bson.M{
			"liquidacion_error": diagnostico,
			"liquidacion_reintento_en": siguiente,
		},
		"$unset": bson.M{
			"liquidacion_lease": "",
			"liquidacion_lease_hasta": "",
		},
	})
	if err != nil {
		return err
	}
	if res.ModifiedCount != 1 {
		return errors.New("lease perdido antes de registrar el reintento")
	}
	return nil
}

func entero(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		if n == float64(int(n)) {
			return int(n)
		}
	}
	return 0
}

func texto(v any) string {
	s, _ := v.(string)
	return s
}
