package mercados

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Adaptador a MongoDB. Respeta el validador de la coleccion "mercados"
// (db/mongo/init/01-init-collections.js): _id es ObjectId y los campos de la
// liquidacion (resultado, puntos_otorgados, liquidado_en) nacen en null.

type repoMongo struct {
	mercados *mongo.Collection
	carreras *mongo.Collection
}

func NuevoRepoMongo(db *mongo.Database) *repoMongo {
	return &repoMongo{mercados: db.Collection("mercados"), carreras: db.Collection("carreras")}
}

// AsegurarIndices crea un indice unico parcial sobre los mercados abiertos:
// asi la base de datos tambien impide que haya dos abiertos a la vez, aunque
// corran varias instancias del motor. Es idempotente.
func (r *repoMongo) AsegurarIndices(ctx context.Context) error {
	_, err := r.mercados.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "estado", Value: 1}},
		Options: options.Index().
			SetName("uq_un_mercado_abierto").
			SetUnique(true).
			SetPartialFilterExpression(bson.M{"estado": EstadoAbierto}),
	})
	return err
}

type docMercado struct {
	ID           primitive.ObjectID `bson:"_id"`
	CarreraID    string             `bson:"carrera_id"`
	Tipo         string             `bson:"tipo"`
	Pregunta     string             `bson:"pregunta"`
	Opciones     []string           `bson:"opciones"`
	Estado       string             `bson:"estado"`
	AbreEn       time.Time          `bson:"abre_en"`
	CierraEn     time.Time          `bson:"cierra_en"`
	ObservaHasta time.Time          `bson:"observa_hasta"`
	CreadoPor    string             `bson:"creado_por"`
}

func (d docMercado) aMercado() Mercado {
	return Mercado{
		ID:           d.ID.Hex(),
		CarreraID:    d.CarreraID,
		Tipo:         d.Tipo,
		Pregunta:     d.Pregunta,
		Opciones:     d.Opciones,
		Estado:       d.Estado,
		AbreEn:       d.AbreEn.UTC(),
		CierraEn:     d.CierraEn.UTC(),
		ObservaHasta: d.ObservaHasta.UTC(),
		CreadoPor:    d.CreadoPor,
	}
}

func (r *repoMongo) CarreraMasReciente(ctx context.Context) (string, error) {
	var doc struct {
		CarreraID string `bson:"carrera_id"`
	}
	opts := options.FindOne().SetSort(bson.D{{Key: "creada_en", Value: -1}})
	if err := r.carreras.FindOne(ctx, bson.M{}, opts).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", ErrSinCarrera
		}
		return "", err
	}
	return doc.CarreraID, nil
}

func (r *repoMongo) ExisteCarrera(ctx context.Context, carreraID string) (bool, error) {
	n, err := r.carreras.CountDocuments(ctx, bson.M{"carrera_id": carreraID}, options.Count().SetLimit(1))
	return n > 0, err
}

func (r *repoMongo) Insertar(ctx context.Context, m Mercado) (string, error) {
	id := primitive.NewObjectID()
	_, err := r.mercados.InsertOne(ctx, bson.M{
		"_id":              id,
		"carrera_id":       m.CarreraID,
		"tipo":             m.Tipo,
		"pregunta":         m.Pregunta,
		"opciones":         m.Opciones,
		"estado":           m.Estado,
		"abre_en":          m.AbreEn,
		"cierra_en":        m.CierraEn,
		"observa_hasta":    m.ObservaHasta,
		"resultado":        nil,
		"puntos_otorgados": nil,
		"creado_por":       m.CreadoPor,
		"creado_en":        m.AbreEn,
		"liquidado_en":     nil,
	})
	if mongo.IsDuplicateKeyError(err) {
		return "", ErrYaHayMercadoAbierto // lo impidio el indice uq_un_mercado_abierto
	}
	if err != nil {
		return "", err
	}
	return id.Hex(), nil
}

func (r *repoMongo) Actual(ctx context.Context) (Mercado, error) {
	var doc docMercado
	opts := options.FindOne().SetSort(bson.D{{Key: "creado_en", Value: -1}})
	if err := r.mercados.FindOne(ctx, bson.M{}, opts).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Mercado{}, ErrNoHayMercado
		}
		return Mercado{}, err
	}
	return doc.aMercado(), nil
}

func (r *repoMongo) Pendientes(ctx context.Context) ([]Mercado, error) {
	cur, err := r.mercados.Find(ctx, bson.M{"estado": bson.M{"$in": []string{EstadoAbierto, EstadoCerrado}}})
	if err != nil {
		return nil, err
	}
	var docs []docMercado
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]Mercado, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.aMercado())
	}
	return out, nil
}

// Cerrar es una actualizacion condicional: solo cambia el documento si sigue
// abierto, por eso es segura aunque se llame dos veces.
func (r *repoMongo) Cerrar(ctx context.Context, id string) (bool, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return false, err
	}
	res, err := r.mercados.UpdateOne(ctx,
		bson.M{"_id": oid, "estado": EstadoAbierto},
		bson.M{"$set": bson.M{"estado": EstadoCerrado}},
	)
	if err != nil {
		return false, err
	}
	return res.ModifiedCount == 1, nil
}
