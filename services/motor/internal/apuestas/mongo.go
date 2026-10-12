package apuestas

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// Adaptadores a MongoDB. Respetan los validadores de db/mongo/init:
// mercados._id es ObjectId y apuestas_contexto.monto es int32.

type mercadosMongo struct{ col *mongo.Collection }

func NuevosMercadosMongo(db *mongo.Database) Mercados {
	return &mercadosMongo{col: db.Collection("mercados")}
}

func (r *mercadosMongo) Obtener(ctx context.Context, id string) (Mercado, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return Mercado{}, ErrMercadoNoExiste
	}
	var doc struct {
		Estado   string    `bson:"estado"`
		Opciones []string  `bson:"opciones"`
		CierraEn time.Time `bson:"cierra_en"`
	}
	if err := r.col.FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Mercado{}, ErrMercadoNoExiste
		}
		return Mercado{}, err
	}
	return Mercado{ID: id, Estado: doc.Estado, Opciones: doc.Opciones, CierraEn: doc.CierraEn}, nil
}

type contextosMongo struct{ col *mongo.Collection }

func NuevosContextosMongo(db *mongo.Database) Contextos {
	return &contextosMongo{col: db.Collection("apuestas_contexto")}
}

func (r *contextosMongo) Guardar(ctx context.Context, c Contexto) error {
	oid, err := primitive.ObjectIDFromHex(c.MercadoID)
	if err != nil {
		return ErrMercadoNoExiste
	}
	_, err = r.col.InsertOne(ctx, bson.M{
		"apuesta_id": c.ApuestaID,
		"mercado_id": oid,
		"usuario_id": c.UsuarioID,
		"opcion":     c.Opcion,
		"monto":      int32(c.Monto), // el validador exige int32
		"creada_en":  c.CreadaEn,
		"liquidada":  false,
		"gano":       nil,
		"puntos_obtenidos": float64(0),
	})
	// Reintento con la misma clave: el indice unico de apuesta_id ya la tiene.
	if mongo.IsDuplicateKeyError(err) {
		return nil
	}
	return err
}
