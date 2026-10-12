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

// mongoStore es el Store real. Solo LEE eventos_carrera y reproducciones (las
// escribe la reproduccion, ingest/replay.go); en mercados y apuestas_contexto
// escribe el lease, los reintentos y los resultados.
type mongoStore struct {
	mercados       *mongo.Collection
	eventos        *mongo.Collection
	contextos      *mongo.Collection
	reproducciones *mongo.Collection
}

func NuevoMongoStore(db *mongo.Database) Store {
	return &mongoStore{
		mercados:       db.Collection("mercados"),
		eventos:        db.Collection("eventos_carrera"),
		contextos:      db.Collection("apuestas_contexto"),
		reproducciones: db.Collection("reproducciones"),
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

func (s *mongoStore) MercadosVencidos(ctx context.Context, ahora time.Time) ([]Mercado, error) {
	filtro := bson.M{
		"estado":        "cerrado",
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
		"liquidacion_lease":       token,
		"liquidacion_lease_hasta": ahora.Add(duracion),
	}})
	if err != nil {
		return "", false, err
	}
	return token, resultado.ModifiedCount == 1, nil
}

// Datos arma lo que Resolver necesita para la ventana [cierra_en,
// observa_hasta] del mercado (limites incluidos). Las dos consultas usan
// fechas REALES escritas por la reproduccion (ingest/replay.go), nunca el
// timestamp historico de los eventos.
func (s *mongoStore) Datos(ctx context.Context, mercado Mercado) (DatosResolucion, error) {
	datos := DatosResolucion{}

	// 1. ¿Se emitio algun evento de posicion de esta carrera en la ventana?
	// Basta con saber si hay uno (SetLimit(1)); usa el indice
	// {carrera_id, tipo, emitido_en} de eventos_carrera.
	n, err := s.eventos.CountDocuments(ctx, bson.M{
		"carrera_id": mercado.CarreraID,
		"tipo":       "posicion",
		"emitido_en": bson.M{"$gte": mercado.CierraEn, "$lte": mercado.ObservaHasta},
	}, options.Count().SetLimit(1))
	if err != nil {
		return datos, fmt.Errorf("contar eventos en la ventana: %w", err)
	}
	datos.HuboEventoPosicion = n > 0
	if datos.HuboEventoPosicion {
		return datos, nil // gana "Sí"; no hace falta mirar el latido
	}

	// 2. Sin eventos: ¿la reproduccion estuvo viva TODA la ventana? Ya corria
	// en cierra_en y su ultimo latido es posterior a observa_hasta. Si se
	// reinicio en medio, su iniciada_en es posterior a cierra_en y no cuenta.
	n, err = s.reproducciones.CountDocuments(ctx, bson.M{
		"_id":         mercado.CarreraID,
		"iniciada_en": bson.M{"$lte": mercado.CierraEn},
		"latido_en":   bson.M{"$gte": mercado.ObservaHasta},
	}, options.Count().SetLimit(1))
	if err != nil {
		return datos, fmt.Errorf("leer latido de la reproduccion: %w", err)
	}
	datos.ReproduccionCubre = n > 0
	return datos, nil
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
			ApuestaID string  `bson:"apuesta_id"`
			UsuarioID string  `bson:"usuario_id"`
			Opcion    string  `bson:"opcion"`
			Monto     int64   `bson:"monto"`
			Liquidada bool    `bson:"liquidada"`
			Gano      *bool   `bson:"gano"`
			Puntos    float64 `bson:"puntos_obtenidos"`
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
		"liquidada":        true,
		"gano":             gano,
		"puntos_obtenidos": puntos,
	}})
	if err != nil {
		return err
	}
	if resultado.ModifiedCount == 1 {
		return nil
	}
	var previo struct {
		Liquidada bool    `bson:"liquidada"`
		Gano      *bool   `bson:"gano"`
		Puntos    float64 `bson:"puntos_obtenidos"`
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
			"estado":           "liquidado",
			"resultado":        resultado.Resultado,
			"puntos_otorgados": resultado.PuntosOtorgados,
			"liquidado_en":     ahora,
		},
		"$unset": bson.M{
			"liquidacion_error":        "",
			"liquidacion_reintento_en": "",
			"liquidacion_lease":        "",
			"liquidacion_lease_hasta":  "",
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
			"liquidacion_error":        diagnostico,
			"liquidacion_reintento_en": siguiente,
		},
		"$unset": bson.M{
			"liquidacion_lease":       "",
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
